package parse

import (
	"os"
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestParse_BelongsToFieldOptions asserts that the options a scalar field
// accepts are honoured on a belongsTo too.
//
// "an article must have an author" is the most common shape in a real schema,
// and every one of these branches was uncovered before this test existed: the
// golden fixture list was hardcoded, so a fixture nobody registered ran
// nothing. An empty .diag proves only that nothing was reported — it says
// nothing about whether `required: true` was applied, which is what this
// asserts.
func TestParse_BelongsToFieldOptions(t *testing.T) {
	src, err := os.ReadFile("testdata/belongs_to_field_options.yaml")
	if err != nil {
		t.Fatal(err)
	}

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: src})
	if len(diags) != 0 {
		t.Fatalf("Parse produced %d diagnostics, want 0:\n%s",
			len(diags), diags.Render(source.File{Name: "lapigo.yaml", Src: src}))
	}

	article := schema.Lookup("article")
	if article == nil {
		t.Fatal(`schema.Lookup("article") = nil`)
	}

	t.Run("field options", func(t *testing.T) {
		for _, tc := range []struct {
			field                                     string
			nullable, unique, readOnly, immutable, pk bool
		}{
			{field: "author", nullable: false, immutable: true},
			{field: "editor", nullable: true, unique: true, readOnly: true},
		} {
			t.Run(tc.field, func(t *testing.T) {
				f := article.Lookup(tc.field)
				if f == nil {
					t.Fatalf("article.Lookup(%q) = nil", tc.field)
				}
				if f.Nullable != tc.nullable {
					t.Errorf("Nullable = %v, want %v", f.Nullable, tc.nullable)
				}
				if f.Unique != tc.unique {
					t.Errorf("Unique = %v, want %v", f.Unique, tc.unique)
				}
				if f.ReadOnly != tc.readOnly {
					t.Errorf("ReadOnly = %v, want %v", f.ReadOnly, tc.readOnly)
				}
				if f.Immutable != tc.immutable {
					t.Errorf("Immutable = %v, want %v", f.Immutable, tc.immutable)
				}
				if f.PK != tc.pk {
					t.Errorf("PK = %v, want %v", f.PK, tc.pk)
				}
			})
		}
	})

	t.Run("on_delete", func(t *testing.T) {
		want := map[string]string{"author": "CASCADE", "editor": "SET NULL"}
		if len(article.Relations) != len(want) {
			t.Fatalf("article has %d relations, want %d", len(article.Relations), len(want))
		}
		for _, rel := range article.Relations {
			w, ok := want[rel.Name]
			if !ok {
				t.Errorf("unexpected relation %q", rel.Name)
				continue
			}
			if rel.OnDelete != w {
				t.Errorf("relation %q OnDelete = %q, want %q", rel.Name, rel.OnDelete, w)
			}
		}
	})

	t.Run("relation targets resolve by identity", func(t *testing.T) {
		user := schema.Lookup("user")
		if user == nil {
			t.Fatal(`schema.Lookup("user") = nil`)
		}
		for _, rel := range article.Relations {
			// Identity, not name: a target compared by name would also pass
			// against a decoy entity, which is the failure Freeze exists to
			// catch.
			if rel.Target != user {
				t.Errorf("relation %q Target is not the same *Entity as schema.Lookup(%q)", rel.Name, "user")
			}
		}
	})

	t.Run("survives Freeze", func(t *testing.T) {
		if err := schema.Freeze(); err != nil {
			t.Errorf("Freeze() = %v, want nil", err)
		}
	})

	var _ *ir.Schema = schema
}
