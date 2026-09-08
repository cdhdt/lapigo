package ddl

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/parse"
	"github.com/cdhdt/lapigo/internal/source"
	"github.com/cdhdt/lapigo/internal/validate"
)

// parseFixture parses and validates a testdata fixture, failing the test on
// any diagnostic Emit's contract forbids.
func parseFixture(t *testing.T, name string) (*source.File, []byte) {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	f := source.File{Name: "lapigo.yaml", Src: src}
	schema, diags := parse.Parse(f)
	if len(diags) != 0 {
		t.Fatalf("fixture %s.yaml must parse cleanly; got:\n%s", name, diags.Render(f))
	}
	if err := validate.Validate(schema, f.Name).Err(); err != nil {
		t.Fatalf("fixture %s.yaml must validate cleanly; got: %v", name, err)
	}
	return &f, Emit(schema)
}

func TestEmit_NilSchema(t *testing.T) {
	if got := Emit(nil); got != nil {
		t.Fatalf("Emit(nil) = %q, want nil", got)
	}
}

// TestEmit_Deterministic is spec §5.3's rule for SQL exactly as for Go:
// twenty runs over the same schema must agree byte for byte. Emit builds
// every name and statement from sorted, slice-ordered IR data -- a map
// ranged into the output would surface here as a flaky mismatch.
func TestEmit_Deterministic(t *testing.T) {
	_, first := parseFixture(t, "canonical")
	for i := 0; i < 20; i++ {
		src, err := os.ReadFile(filepath.Join("testdata", "canonical.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		f := source.File{Name: "lapigo.yaml", Src: src}
		schema, _ := parse.Parse(f)
		if got := Emit(schema); !bytes.Equal(got, first) {
			t.Fatalf("run %d differs from run 0:\n got:\n%s\nfirst:\n%s", i+1, got, first)
		}
	}
}

// TestIndexName_TruncationStaysDistinct pins the 63-byte rule: two index
// names sharing a 63-byte prefix must both come back within the limit and
// must differ, deterministically -- Postgres would silently truncate both to
// the same identifier and reject the second CREATE INDEX at apply time.
func TestIndexName_TruncationStaysDistinct(t *testing.T) {
	em := &emitter{names: make(map[string]bool)}
	long := strings.Repeat("x", 60)

	first := em.indexName("t", []string{long + "a", "id"})
	second := em.indexName("t", []string{long + "b", "id"})

	if len(first) > maxIdentifierBytes {
		t.Errorf("first name is %d bytes, want at most %d: %q", len(first), maxIdentifierBytes, first)
	}
	if len(second) > maxIdentifierBytes {
		t.Errorf("second name is %d bytes, want at most %d: %q", len(second), maxIdentifierBytes, second)
	}
	if first == second {
		t.Fatalf("distinct column lists produced the same name %q", first)
	}

	// The rule is deterministic: a fresh emitter must derive the same pair.
	em2 := &emitter{names: make(map[string]bool)}
	if got := em2.indexName("t", []string{long + "a", "id"}); got != first {
		t.Errorf("re-derived first name = %q, want %q", got, first)
	}
	if got := em2.indexName("t", []string{long + "b", "id"}); got != second {
		t.Errorf("re-derived second name = %q, want %q", got, second)
	}
}

// TestIndexName_JoinAmbiguityCollisions pins the other collision source: a
// column literally named "a_b" and a two-column list [a, b] join to the same
// base name. The second registration must disambiguate with the hash suffix
// rather than emit a duplicate the database would reject.
func TestIndexName_JoinAmbiguityCollisions(t *testing.T) {
	em := &emitter{names: make(map[string]bool)}

	first := em.indexName("t", []string{"a_b", "id"})
	second := em.indexName("t", []string{"a", "b", "id"})

	if first != "t_a_b_id_idx" {
		t.Errorf("first name = %q, want t_a_b_id_idx", first)
	}
	if second == first {
		t.Fatalf("join-ambiguous lists produced the same name %q", second)
	}
	if !strings.HasPrefix(second, "t_a_b_id") {
		t.Errorf("disambiguated name %q lost its readable prefix", second)
	}
	if len(second) > maxIdentifierBytes {
		t.Errorf("disambiguated name is %d bytes, want at most %d", len(second), maxIdentifierBytes)
	}
}

// TestFitName pins the derived-name truncation rule on its own, apart from
// the golden files: a fitting name passes through untouched, an over-long
// name comes back exactly 63 bytes, and two names sharing a 63-byte prefix
// come back distinct -- deterministically, since the same input must always
// produce the same output across runs and machines.
func TestFitName(t *testing.T) {
	short := fitName("articles_pkey")
	if short != "articles_pkey" {
		t.Errorf("fitName shortened a name that already fits: %q", short)
	}

	// 63 shared bytes + one distinguishing byte = 64, one over the limit.
	prefix := strings.Repeat("x", 63)
	first := fitName(prefix + "a")
	second := fitName(prefix + "b")

	if len(first) != maxIdentifierBytes {
		t.Errorf("first name is %d bytes, want exactly %d: %q", len(first), maxIdentifierBytes, first)
	}
	if first == second {
		t.Fatalf("prefix-sharing names truncated to the same identifier %q", first)
	}
	if fitName(prefix+"a") != first || fitName(prefix+"b") != second {
		t.Error("fitName is not deterministic across calls")
	}
}

// TestQuoteSQL pins the escaping of every SQL string literal this package
// emits: single quotes double, nothing else changes. Enum members and
// `default:` literals are the only values reaching it, and the parser has
// already rejected control characters in enum members.
func TestQuoteSQL(t *testing.T) {
	cases := []struct{ in, want string }{
		{"draft", "'draft'"},
		{"it's", "'it''s'"},
		{"", "''"},
		{"a'b'c", "'a''b''c'"},
	}
	for _, c := range cases {
		if got := quoteSQL(c.in); got != c.want {
			t.Errorf("quoteSQL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestEmit_SkipsPrimaryKeyOnlyIndex pins the skip rule: an entity whose only
// index column list is the primary key's own must emit no CREATE INDEX -- the
// pkey constraint already created exactly that index, and a second one would
// double the write cost of every insert for zero read benefit.
func TestEmit_SkipsPrimaryKeyOnlyIndex(t *testing.T) {
	src := `entities:
  thing:
    fields:
      id: { type: uuid, pk: true }
      label: { type: text, required: true }
`
	f := source.File{Name: "lapigo.yaml", Src: []byte(src)}
	schema, diags := parse.Parse(f)
	if len(diags) != 0 {
		t.Fatalf("want clean parse, got:\n%s", diags.Render(f))
	}
	if err := validate.Validate(schema, f.Name).Err(); err != nil {
		t.Fatalf("want clean validation, got: %v", err)
	}

	got := string(Emit(schema))
	if strings.Contains(got, "CREATE INDEX") {
		t.Errorf("emitted an index for a primary-key-only sort:\n%s", got)
	}
	want := `-- Initial schema generated by lapigo. DO NOT EDIT.

CREATE TABLE "things" (
    "id" uuid NOT NULL,
    "label" text NOT NULL,
    CONSTRAINT things_pkey PRIMARY KEY ("id")
);
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
