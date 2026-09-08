package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// parseIndexesSrc is the inline counterpart of testdata/declared_indexes.yaml,
// kept here as source text so the span assertions below can name a line and
// column that cannot drift out from under them by an edit to a fixture file.
const parseIndexesSrc = `entities:
  article:
    fields:
      id: { type: uuid, pk: true }
      status: { type: enum, values: [draft, published], required: true }
      author: { type: belongsTo, target: user }
    filters: [status, author]
    indexes:
      - filters: [status, author]
  user:
    fields:
      id: { type: uuid, pk: true }
`

// TestParse_Indexes_ResolvedIR asserts what `indexes:` must resolve to: one
// ir.Index per entry, its columns resolved to the same *ir.Field pointers
// Entity.Lookup hands out (identity, not a copy), in declaration order, each
// carrying the span of its own entry in the `filters:` list.
func TestParse_Indexes_ResolvedIR(t *testing.T) {
	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(parseIndexesSrc)})
	if len(diags) != 0 {
		t.Fatalf("want zero diagnostics, got:\n%s", diags.Render(source.File{Name: "lapigo.yaml", Src: []byte(parseIndexesSrc)}))
	}
	if schema == nil {
		t.Fatal("schema is nil with zero diagnostics")
	}

	article := schema.Lookup("article")
	if article == nil {
		t.Fatal("entity article is missing")
	}
	if len(article.Indexes) != 1 {
		t.Fatalf("want exactly 1 declared index, got %d", len(article.Indexes))
	}

	idx := article.Indexes[0]
	if len(idx.Columns) != 2 {
		t.Fatalf("want 2 columns in the declared index, got %d", len(idx.Columns))
	}

	// Identity, not just name: ir.Schema.Freeze checks IndexColumn.Field by
	// pointer, so a copy constructed during resolution would pass a
	// name-based reading of this test while failing the contract the IR
	// states (spec §2.2: "sort keys and filters hold resolved *Field
	// pointers, not names" -- declared index columns hold the same).
	status := article.Lookup("status")
	author := article.Lookup("author")
	if status == nil || author == nil {
		t.Fatalf("lookup failed: status=%v author=%v", status, author)
	}
	if idx.Columns[0].Field != status {
		t.Errorf("column 0 does not resolve to the status field pointer")
	}
	if idx.Columns[1].Field != author {
		t.Errorf("column 1 does not resolve to the author field pointer")
	}

	// The spans blame the `filters:` entries of the `indexes:` item: on
	// line 9 (`      - filters: [status, author]`), "status" occupies
	// columns 19-25 and "author" columns 27-33 (End exclusive). Asserting
	// both ends catches a span recomputed from the field's own declaration
	// instead of the index entry (spec §2.2's reconstruction hazard, the
	// same one Filter.Span exists for).
	wantFirst := source.Span{Start: source.Pos{Line: 9, Column: 19}, End: source.Pos{Line: 9, Column: 25}}
	wantSecond := source.Span{Start: source.Pos{Line: 9, Column: 27}, End: source.Pos{Line: 9, Column: 33}}
	if idx.Columns[0].Span != wantFirst {
		t.Errorf("column 0 span = %+v, want %+v", idx.Columns[0].Span, wantFirst)
	}
	if idx.Columns[1].Span != wantSecond {
		t.Errorf("column 1 span = %+v, want %+v", idx.Columns[1].Span, wantSecond)
	}

	// An entity with no `indexes:` key declares none: the derived set of
	// spec §7.2 is computed by the DDL emitter, never stored on the IR.
	user := schema.Lookup("user")
	if user == nil {
		t.Fatal("entity user is missing")
	}
	if len(user.Indexes) != 0 {
		t.Errorf("entity user declares no indexes, got %d", len(user.Indexes))
	}
}

// TestParse_Indexes_EntityKeyOrderingSuggestion asserts a typo of the
// entity-level `indexes:` key itself is caught by the entity key whitelist
// with a suggestion, the same way a typo of `filters:` at entity level is.
func TestParse_Indexes_EntityKeyOrderingSuggestion(t *testing.T) {
	src := `entities:
  article:
    fields:
      id: { type: uuid, pk: true }
    index: []
`
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	want := `unknown key "index" in entity ` + "`article`"
	found := false
	for _, d := range diags {
		if d.Message == want && d.Hint == "did you mean `indexes`?" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no unknown-key diagnostic with suggestion among:\n%s",
			diags.Render(source.File{Name: "lapigo.yaml", Src: []byte(src)}))
	}
}
