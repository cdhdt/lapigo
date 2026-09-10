package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

func TestEntity_Lookup_Found(t *testing.T) {
	id := &Field{Name: source.Bare("id"), PK: true}
	title := &Field{Name: source.Bare("title")}
	e := &Entity{Name: "article", Fields: []*Field{id, title}}

	got := e.Lookup("title")

	if got != title {
		t.Fatalf("Lookup(\"title\") = %p, want %p (the same *Field, not a copy)", got, title)
	}
}

func TestEntity_Lookup_NotFound(t *testing.T) {
	e := &Entity{Name: "article", Fields: []*Field{{Name: source.Bare("id")}}}

	if got := e.Lookup("nonexistent"); got != nil {
		t.Errorf("Lookup(\"nonexistent\") = %+v, want nil", got)
	}
}

func TestSchema_Lookup_Found(t *testing.T) {
	article := &Entity{Name: "article"}
	user := &Entity{Name: "user"}
	s := &Schema{Entities: []*Entity{article, user}}

	got := s.Lookup("user")

	// Assert pointer identity, not just Name: returning a copy with a
	// matching Name would pass a weaker assertion but breaks every
	// downstream pointer that expects Lookup to hand back the schema's own
	// *Entity.
	if got != user {
		t.Fatalf("Lookup(\"user\") = %p, want %p (the same *Entity, not a copy)", got, user)
	}
}

func TestSchema_Lookup_NotFound(t *testing.T) {
	s := &Schema{Entities: []*Entity{{Name: "article"}}}

	if got := s.Lookup("nonexistent"); got != nil {
		t.Errorf("Lookup(\"nonexistent\") = %+v, want nil", got)
	}
}

// TestSchema_Lookup_NilReceiver pins that Lookup on a nil *Schema returns nil
// rather than panicking. CLAUDE.md forbids panics in library code.
func TestSchema_Lookup_NilReceiver(t *testing.T) {
	var s *Schema

	if got := s.Lookup("anything"); got != nil {
		t.Errorf("Lookup on a nil Schema = %+v, want nil, not a panic", got)
	}
}

// TestEntity_SortedFilters_ReordersByName pins spec §7.4's canonical cursor
// fingerprint form: filters sorted by field name. Filters is declared out of
// name order here specifically so a SortedFilters that forgot to sort, or
// that sorted by declaration position instead of name, would fail this.
func TestEntity_SortedFilters_ReordersByName(t *testing.T) {
	zeta := &Field{Name: source.Bare("zeta")}
	alpha := &Field{Name: source.Bare("alpha")}
	mid := &Field{Name: source.Bare("mid")}
	e := &Entity{
		Name: "article",
		Filters: []Filter{
			{Field: zeta},
			{Field: alpha},
			{Field: mid},
		},
	}

	got := e.SortedFilters()

	if len(got) != 3 || got[0].Field != alpha || got[1].Field != mid || got[2].Field != zeta {
		t.Fatalf("SortedFilters() = %v, want [alpha, mid, zeta] by field name", filterNames(got))
	}
}

// TestEntity_SortedFilters_LeavesDeclarationOrderUntouched pins that
// SortedFilters returns a separate view: internal/ddl's indexColumnLists
// depends on Entity.Filters staying in declaration order (each declared
// filter becomes one derived index, spec §7.2), so a SortedFilters that
// sorted e.Filters in place would silently reorder the emitted index set.
func TestEntity_SortedFilters_LeavesDeclarationOrderUntouched(t *testing.T) {
	zeta := &Field{Name: source.Bare("zeta")}
	alpha := &Field{Name: source.Bare("alpha")}
	e := &Entity{
		Name:    "article",
		Filters: []Filter{{Field: zeta}, {Field: alpha}},
	}

	_ = e.SortedFilters()

	if e.Filters[0].Field != zeta || e.Filters[1].Field != alpha {
		t.Fatalf("Filters after SortedFilters() = %v, want declaration order [zeta, alpha] untouched", filterNames(e.Filters))
	}
}

func filterNames(fs []Filter) []string {
	names := make([]string, len(fs))
	for i, f := range fs {
		names[i] = f.Field.Name.Value
	}
	return names
}
