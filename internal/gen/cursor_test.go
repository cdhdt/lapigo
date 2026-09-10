package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/parse"
	"github.com/cdhdt/lapigo/internal/source"
	"github.com/cdhdt/lapigo/internal/validate"
)

// TestCanonicalCursorShape_Full pins the canonical form spec §7.4 requires
// the cursor fingerprint to be computed over, whole and to the byte.
//
// Whole, because it IS the contract: the fingerprint is what rejects a
// cursor minted against another entity or against an older shape of this
// one, and any change to these bytes silently invalidates every cursor in
// flight. A substring assertion here would pass on a form that had lost the
// entity name -- revision 1's defect exactly (spec §7.4, §12).
//
// The filter order is the one spec §7.4 spells out -- "filters sorted by
// name" -- which is ir.Entity.SortedFilters, not e.Filters: the fixture
// declares `filters: [status, author]` in that order and the canonical form
// carries author's column first.
func TestCanonicalCursorShape_Full(t *testing.T) {
	e := loadFixture(t, "full").Lookup("article")
	if e == nil {
		t.Fatal(`the full fixture must declare an entity named "article"`)
	}

	got := canonicalCursorShape(e)
	const want = "lapigo-cursor-fingerprint-v1\n" +
		"entity=article\n" +
		"sort=-created_at,-id\n" +
		"filters=author_id:eq,status:eq\n"
	if got != want {
		t.Errorf("canonicalCursorShape(article) =\n%q\nwant\n%q", got, want)
	}
}

// TestCursorFingerprint_Full pins the constant the generated code carries,
// literally. It is the value baked into every cursor the generated store
// mints, so it is asserted as a whole literal and not recomputed here from
// canonicalCursorShape: a test that hashes the same input the
// implementation does passes on any hash, including one that dropped the
// entity name on the way in.
func TestCursorFingerprint_Full(t *testing.T) {
	e := loadFixture(t, "full").Lookup("article")
	if e == nil {
		t.Fatal(`the full fixture must declare an entity named "article"`)
	}

	got := cursorFingerprint(e)
	const want = "438049d90cc210822a2f11aa70026b89"
	if got != want {
		t.Errorf("cursorFingerprint(article) = %q, want %q", got, want)
	}
}

// TestCursorFingerprint_DiffersByEntityAlone is spec §7.4's own reason for
// putting the entity name in the fingerprint: revision 1 omitted it, so two
// entities sharing a sort and a filter shape produced interchangeable
// cursors and a cursor from one replayed against the other.
//
// The two entities below are byte-identical apart from their names.
func TestCursorFingerprint_DiffersByEntityAlone(t *testing.T) {
	schema := loadSchemaFromYAML(t, `
entities:
  alpha:
    fields:
      id:         { type: uuid, pk: true }
      created_at: { type: timestamp, required: true }
      status:     { type: string, required: true }
    sort: [-created_at, -id]
    filters: [status]
    endpoints: [list]
  beta:
    fields:
      id:         { type: uuid, pk: true }
      created_at: { type: timestamp, required: true }
      status:     { type: string, required: true }
    sort: [-created_at, -id]
    filters: [status]
    endpoints: [list]
`)

	alpha, beta := schema.Lookup("alpha"), schema.Lookup("beta")
	if alpha == nil || beta == nil {
		t.Fatal("both entities must resolve")
	}
	if got, want := cursorFingerprint(alpha), "f02e9457af91a0ad10a91b2ab1dd8ed7"; got != want {
		t.Errorf("cursorFingerprint(alpha) = %q, want %q", got, want)
	}
	if cursorFingerprint(alpha) == cursorFingerprint(beta) {
		t.Errorf("alpha and beta share the fingerprint %q, so a cursor from one is accepted by the "+
			"other: the entity name is missing from the canonical form (spec §7.4)",
			cursorFingerprint(alpha))
	}
}

// loadSchemaFromYAML parses, validates and freezes src, the way loadFixture
// does for a file under testdata/.
//
// It exists because testdata/ is not free real estate: golden_test.go's
// orphan guard fails on a fixture no golden case renders, and a schema this
// package needs only to compare two fingerprints has no business growing
// the golden corpus. Same pipeline, same strictness -- Generate's contract
// is a frozen, validated *ir.Schema (spec §2.1).
func loadSchemaFromYAML(t *testing.T, src string) *ir.Schema {
	t.Helper()

	f := source.File{Name: "lapigo.yaml", Src: []byte(src)}
	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("the inline schema must parse cleanly; got:\n%s", parseDiags.Render(f))
	}
	diags := validate.Validate(schema, f.Name)
	if err := diags.Err(); err != nil {
		t.Fatalf("the inline schema must carry no error-severity diagnostics; got:\n%s", diags.Render(f))
	}
	if err := schema.Freeze(); err != nil {
		t.Fatalf("the inline schema must freeze: %v", err)
	}
	return schema
}

// TestPlan_StoreCursorFile pins the cursor file's whole plan entry for each
// fixture: its path, its package, the template that renders it and -- the
// part a wrong answer turns into generated code that does not compile --
// its exact import set.
//
// The import set is per SCHEMA, not per entity: one file carries every
// listing entity's codec, so `time` is in it when some entity sorts on a
// timestamp and out of it when none does. list_only is what pins the
// "out" direction; without a fixture whose sort keys need no time import,
// a surplus import would compile everywhere the corpus looks.
func TestPlan_StoreCursorFile(t *testing.T) {
	base := []string{"bytes", "encoding/base64", "encoding/json", "errors", "fmt"}

	cases := []struct {
		fixture string
		want    []string // the complete import set, unsorted; nil means "no file at all"
	}{
		{
			// article sorts on created_at (timestamp -> time) and id
			// (uuid -> pgtype); user sorts on id.
			fixture: "full",
			want: append(append([]string{}, base...),
				"time", pgtypeImport, modelImportPath(testModulePath)),
		},
		{
			// note sorts on id alone: a uuid key, so pgtype but no time.
			fixture: "list_only",
			want: append(append([]string{}, base...),
				pgtypeImport, modelImportPath(testModulePath)),
		},
		{
			// event declares no `list`, so nothing in the schema paginates
			// and the file is not planned at all.
			fixture: "no_list",
			want:    nil,
		},
	}

	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			files, err := plan(loadFixture(t, c.fixture), testModulePath)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}

			var got *OutputFile
			for i, f := range files {
				if f.Path == "internal/gen/store/cursor.go" {
					got = &files[i]
				}
			}

			if c.want == nil {
				if got != nil {
					t.Fatalf("plan(%s) includes internal/gen/store/cursor.go, but no entity in that "+
						"fixture declares `list`: a schema that never paginates needs no cursor codec", c.fixture)
				}
				return
			}
			if got == nil {
				t.Fatalf("plan(%s) has no internal/gen/store/cursor.go", c.fixture)
			}
			if got.Package != packageStore {
				t.Errorf("Package = %q, want %q", got.Package, packageStore)
			}
			if got.Template != "store/cursor.go" {
				t.Errorf("Template = %q, want %q", got.Template, "store/cursor.go")
			}
			assertStringSetsEqual(t, "the cursor file's imports", got.Imports, c.want)
		})
	}
}

// TestGeneratedCursor_RoundTrip compiles the generated codec and RUNS it.
//
// It exists because every other test in this package reads bytes. A golden
// file proves the generator emits the text someone reviewed; the compile
// tier proves that text type-checks. Neither of them can tell whether a
// cursor decodes back to the values it was built from, whether a timestamp
// survives to the microsecond, or whether a forged one is refused -- and
// those are the three properties spec §12 records as revision 1's worst
// defects. A codec nobody has round-tripped is a guess.
//
// The mechanism is spec §8's compile tier plus one file: the full fixture's
// output is written into a temp module, a hand-written test file goes in
// beside it in package store -- in package, because every identifier it
// exercises is unexported -- and `go test` runs there. Its own output is
// logged here, so `go test -v ./internal/gen/... -run RoundTrip` prints the
// cursor, the JSON it decodes to, and the rejection reason for every forged
// form.
func TestGeneratedCursor_RoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the cursor round trip under -short: it shells out to `go test`")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain on PATH: %v", err)
	}

	const fixture = "full"
	modulePath := compileModule + "/" + fixture

	dir := t.TempDir()
	writeTempModule(t, dir)

	files, err := Generate(loadFixture(t, fixture), modulePath)
	if err != nil {
		t.Fatalf("Generate %s: %v", fixture, err)
	}
	for p, src := range files {
		dst := filepath.Join(dir, fixture, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	storeDir := filepath.Join(dir, fixture, filepath.FromSlash(genRoot), packageStore)
	// A placeholder, not fmt.Sprintf: the driver is Go source full of format
	// verbs of its own, and every one of them would be a wrong substitution.
	driver := strings.ReplaceAll(cursorRoundTripDriver, modelImportPlaceholder, modelImportPath(modulePath))
	if err := os.WriteFile(filepath.Join(storeDir, "cursor_roundtrip_test.go"), []byte(driver), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "test", "-count=1", "-v", "./"+fixture+"/"+genRoot+"/"+packageStore+"/")
	cmd.Dir = dir
	// Same environment as the compile tier: -mod=mod because the copied
	// go.mod carries requires the generated packages do not all use, and
	// GOPROXY=off because a test whose result depends on the network is not
	// a test of this repository.
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")

	out, err := cmd.CombinedOutput()
	t.Logf("go test in the generated module:\n%s", out)
	if err != nil {
		t.Fatalf("the generated cursor codec does not round-trip: %v", err)
	}
}

// modelImportPlaceholder is what cursorRoundTripDriver carries where the
// model package's import path goes: the path is only known once a module
// path is chosen, and the driver is a literal.
const modelImportPlaceholder = "__MODEL_IMPORT_PATH__"

// cursorRoundTripDriver is the test file written beside the generated
// codec, with modelImportPlaceholder where the model import goes.
//
// It is a Go source literal rather than a file under testdata/ so that it
// travels with the assertions it belongs to: it is written against the full
// fixture's article and user entities -- article's two-key sort over a
// timestamp and a uuid is the only fixture shape that exercises both the
// UTC normalisation and the pgtype validity check -- and a fixture change
// that breaks it should break it in the same file the reviewer is reading.
const cursorRoundTripDriver = `package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"__MODEL_IMPORT_PATH__"
)

// sampleID is the article's primary key throughout, spelled out so the
// expected JSON below can be asserted as one literal.
var sampleID = pgtype.UUID{
	Bytes: [16]byte{0x9f, 0x2c, 0x18, 0x3e, 0x4a, 0x5b, 0x4c, 0x6d, 0x8e, 0x7f, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
	Valid: true,
}

// sampleArticle is a scanned row as pgx would hand it over, with two
// deliberate properties: a sub-second timestamp at MICROSECOND precision,
// which is what timestamptz stores and what a cursor has to carry back
// unchanged, and a zone that is not UTC, which is what pgx produces when a
// pool is built without ScanLocation (spec §7.5 rule 2).
func sampleArticle() *model.Article {
	return &model.Article{
		ID:        sampleID,
		CreatedAt: time.Date(2026, 9, 11, 12, 34, 56, 789012000, time.FixedZone("UTC+2", 2*60*60)),
	}
}

func TestArticleCursor_RoundTrip(t *testing.T) {
	row := sampleArticle()

	cursor, err := encodeArticleCursor(row)
	if err != nil {
		t.Fatalf("encodeArticleCursor: %v", err)
	}
	t.Logf("cursor  = %s", cursor)

	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		t.Fatalf("the cursor must be unpadded base64url: %v", err)
	}
	t.Logf("payload = %s", raw)

	// The whole payload, as one literal. The member order, the member names
	// (columns, spec §6.9.2), the fingerprint and the UTC-normalised
	// timestamp are each part of the contract, and each of them survives a
	// substring assertion that has lost the others.
	const wantPayload = "{\"v\":1,\"f\":\"438049d90cc210822a2f11aa70026b89\"," +
		"\"k\":{\"created_at\":\"2026-09-11T10:34:56.789012Z\"," +
		"\"id\":\"9f2c183e-4a5b-4c6d-8e7f-001122334455\"}}"
	if string(raw) != wantPayload {
		t.Errorf("payload =\n%s\nwant\n%s", raw, wantPayload)
	}

	got, err := decodeArticleCursor(cursor)
	if err != nil {
		t.Fatalf("decodeArticleCursor: %v", err)
	}
	if !got.CreatedAt.Equal(row.CreatedAt) {
		t.Errorf("decoded created_at = %v, want the same instant as %v", got.CreatedAt, row.CreatedAt)
	}
	if want := "2026-09-11T10:34:56.789012Z"; got.CreatedAt.Format(time.RFC3339Nano) != want {
		t.Errorf("decoded created_at = %q, want %q", got.CreatedAt.Format(time.RFC3339Nano), want)
	}
	if got.CreatedAt.Location() != time.UTC {
		t.Errorf("decoded created_at is in %v, want UTC (spec §7.5 rule 2)", got.CreatedAt.Location())
	}
	if got.ID != row.ID {
		t.Errorf("decoded id = %v, want %v", got.ID, row.ID)
	}
	decodedID, err := got.ID.MarshalJSON()
	if err != nil {
		t.Fatalf("marshalling the decoded id: %v", err)
	}
	t.Logf("decoded = %s / %s", got.CreatedAt.Format(time.RFC3339Nano), decodedID)
}

// TestArticleCursor_TrailingZeroSubsecond is spec §7.5 rule 3's hazard,
// pinned as a value round trip: time.Time's JSON encoding strips trailing
// zeros, so ".5" and ".500000" are one cursor and "…30Z" sorts after
// "…30.5Z" as text. Nothing here compares text -- what has to survive is the
// instant.
func TestArticleCursor_TrailingZeroSubsecond(t *testing.T) {
	row := sampleArticle()
	row.CreatedAt = time.Date(2026, 9, 11, 12, 34, 56, 500000000, time.UTC)

	cursor, err := encodeArticleCursor(row)
	if err != nil {
		t.Fatalf("encodeArticleCursor: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(cursor)
	t.Logf("payload = %s", raw)

	got, err := decodeArticleCursor(cursor)
	if err != nil {
		t.Fatalf("decodeArticleCursor: %v", err)
	}
	if !got.CreatedAt.Equal(row.CreatedAt) {
		t.Errorf("decoded created_at = %v, want %v", got.CreatedAt, row.CreatedAt)
	}
	if got.CreatedAt.Nanosecond() != 500000000 {
		t.Errorf("decoded created_at carries %d ns, want 500000000", got.CreatedAt.Nanosecond())
	}
}

// TestArticleCursor_IsDeterministic pins that one row mints one cursor:
// a cursor is a cache key in every deployment that fronts a generated API,
// and two spellings of one position defeat that silently.
func TestArticleCursor_IsDeterministic(t *testing.T) {
	first, err := encodeArticleCursor(sampleArticle())
	if err != nil {
		t.Fatalf("encodeArticleCursor: %v", err)
	}
	for i := 0; i < 8; i++ {
		again, err := encodeArticleCursor(sampleArticle())
		if err != nil {
			t.Fatalf("encodeArticleCursor: %v", err)
		}
		if again != first {
			t.Fatalf("run %d minted %q, want %q", i, again, first)
		}
	}
}

// TestPgtypeUUID_NullIsUnreachableThroughAPointer pins the two dependency
// behaviours the decoder's pgtype validity check is written against, so
// that a change to either shows up here rather than as a NULL reaching a
// keyset seek.
//
// pgtype.UUID.UnmarshalJSON accepts the literal null and reports no error,
// leaving UUID{Valid: false} -- a value that binds as NULL and makes every
// comparison unknown (spec §3.3 rule 3's reasoning). What keeps that out of
// reach today is encoding/json's own rule for a null landing on a pointer
// field: the pointer is set to nil and the Unmarshaler is never called, so
// the decoder's nil check is what rejects it. The validity check behind it
// is the guard for the day one of those two facts changes.
func TestPgtypeUUID_NullIsUnreachableThroughAPointer(t *testing.T) {
	var direct pgtype.UUID
	if err := direct.UnmarshalJSON([]byte("null")); err != nil {
		t.Fatalf("pgtype.UUID.UnmarshalJSON(null) = %v, want no error", err)
	}
	if direct.Valid {
		t.Error("pgtype.UUID.UnmarshalJSON(null) left Valid true")
	}

	var keys articleCursorKeys
	payload := "{\"created_at\":\"2026-09-11T10:34:56.789012Z\",\"id\":null}"
	if err := json.Unmarshal([]byte(payload), &keys); err != nil {
		t.Fatalf("unmarshalling %s: %v", payload, err)
	}
	if keys.ID != nil {
		t.Errorf("id = %+v, want nil: encoding/json must leave a null pointer member nil", keys.ID)
	}
}

func TestArticleCursor_NilRow(t *testing.T) {
	if _, err := encodeArticleCursor(nil); err == nil {
		t.Fatal("encodeArticleCursor(nil) returned no error")
	}
}

// TestArticleCursor_Rejects is the forged-cursor half: every one of these
// must be an error wrapping ErrInvalidCursor -- which httpapi answers 400
// invalid_cursor -- and none of them may panic or return a usable position
// (spec §7.4).
func TestArticleCursor_Rejects(t *testing.T) {
	valid, err := encodeArticleCursor(sampleArticle())
	if err != nil {
		t.Fatalf("encodeArticleCursor: %v", err)
	}
	rawValid, err := base64.RawURLEncoding.DecodeString(valid)
	if err != nil {
		t.Fatalf("decoding the valid cursor: %v", err)
	}

	// A cursor for another entity, minted by that entity's own encoder.
	otherEntity, err := encodeUserCursor(&model.User{ID: sampleID})
	if err != nil {
		t.Fatalf("encodeUserCursor: %v", err)
	}

	// The same article keys under another entity's fingerprint. This is the
	// one that proves the fingerprint is what does the rejecting: the
	// members are exactly what decodeArticleCursor expects, so nothing but
	// the shape check can refuse it (spec §7.4, §12).
	keyCreatedAt := sampleArticle().CreatedAt.UTC()
	keyID := sampleID
	replayed, err := encodeCursor(userCursorFingerprint, articleCursorKeys{CreatedAt: &keyCreatedAt, ID: &keyID})
	if err != nil {
		t.Fatalf("encodeCursor: %v", err)
	}

	cases := []struct {
		name   string
		cursor string
	}{
		{"empty", ""},
		{"not base64url", "!!!not-base64!!!"},
		{"padded base64url", base64.URLEncoding.EncodeToString(rawValid)},
		{"truncated", valid[:len(valid)/2]},
		{"one byte short", valid[:len(valid)-1]},
		// Truncated in the payload rather than in the base64, so that what
		// reaches the JSON decoder is a well-formed base64url string
		// carrying half an object: the base64 alphabet check cannot be what
		// rejects this one.
		{"truncated payload", base64.RawURLEncoding.EncodeToString(rawValid[:len(rawValid)/2])},
		{"another entity's cursor", otherEntity},
		{"another entity's fingerprint", replayed},
		{"tampered fingerprint", mutate(t, rawValid, func(d map[string]any) {
			d["f"] = "00000000000000000000000000000000"
		})},
		{"tampered fingerprint, one character", mutate(t, rawValid, func(d map[string]any) {
			f := d["f"].(string)
			d["f"] = "0" + f[1:]
		})},
		{"future format version", mutate(t, rawValid, func(d map[string]any) { d["v"] = 2 })},
		{"version as a string", mutate(t, rawValid, func(d map[string]any) { d["v"] = "1" })},
		{"timestamp as a number", mutate(t, rawValid, func(d map[string]any) {
			keys(d)["created_at"] = 1789012345
		})},
		{"timestamp as a bare date", mutate(t, rawValid, func(d map[string]any) {
			keys(d)["created_at"] = "2026-09-11"
		})},
		{"timestamp as a bool", mutate(t, rawValid, func(d map[string]any) {
			keys(d)["created_at"] = true
		})},
		{"uuid as a number", mutate(t, rawValid, func(d map[string]any) { keys(d)["id"] = 42 })},
		{"uuid unhyphenated", mutate(t, rawValid, func(d map[string]any) {
			keys(d)["id"] = "9f2c183e4a5b4c6d8e7f001122334455"
		})},
		{"uuid not a uuid", mutate(t, rawValid, func(d map[string]any) { keys(d)["id"] = "nonsense" })},
		{"a key is missing", mutate(t, rawValid, func(d map[string]any) { delete(keys(d), "created_at") })},
		{"a key is null", mutate(t, rawValid, func(d map[string]any) { keys(d)["id"] = nil })},
		{"an unknown key member", mutate(t, rawValid, func(d map[string]any) { keys(d)["title"] = "x" })},
		{"an unknown envelope member", mutate(t, rawValid, func(d map[string]any) { d["extra"] = 1 })},
		{"keys is not an object", mutate(t, rawValid, func(d map[string]any) { d["k"] = "created_at" })},
		{"not a JSON object", base64.RawURLEncoding.EncodeToString([]byte("[1,2,3]"))},
		{"trailing bytes", base64.RawURLEncoding.EncodeToString(append(append([]byte{}, rawValid...), '{', '}'))},
		{"empty JSON object", base64.RawURLEncoding.EncodeToString([]byte("{}"))},
	}

	for _, c := range cases {
		got, err := decodeArticleCursor(c.cursor)
		if err == nil {
			t.Errorf("%s: decoded without an error, giving %+v", c.name, got)
			continue
		}
		if !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s: error %v does not wrap ErrInvalidCursor, so httpapi cannot answer 400", c.name, err)
			continue
		}
		if got != (articleCursor{}) {
			t.Errorf("%s: rejected but returned %+v, want the zero cursor", c.name, got)
		}
		t.Logf("rejected %-36s %v", c.name+":", err)
	}
}

// mutate decodes a cursor payload, applies f to it and re-encodes it, which
// is how a forged cursor is built here: through the same JSON and base64
// the decoder reads, so what the test feeds it is a well-formed cursor
// carrying wrong content rather than random bytes.
func mutate(t *testing.T, payload []byte, f func(map[string]any)) string {
	t.Helper()

	var doc map[string]any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("the valid payload must decode: %v", err)
	}
	f(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("re-encoding the mutated payload: %v", err)
	}
	return base64.RawURLEncoding.EncodeToString(out)
}

// keys returns the payload's k member.
func keys(d map[string]any) map[string]any { return d["k"].(map[string]any) }
`
