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
