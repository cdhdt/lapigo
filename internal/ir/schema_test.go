package ir

import (
	"reflect"
	"testing"
)

func TestEntity_Lookup_Found(t *testing.T) {
	id := Field{Name: Bare("id"), PK: true}
	title := Field{Name: Bare("title")}
	e := &Entity{Name: "article", Fields: []Field{id, title}}

	got := e.Lookup("title")

	if got == nil {
		t.Fatal("Lookup(\"title\") = nil, want the title field")
	}
	if got.Name.Value != "title" {
		t.Errorf("Lookup(\"title\").Name.Value = %q, want %q", got.Name.Value, "title")
	}
	if got != &e.Fields[1] {
		t.Error("Lookup returned a copy, want a pointer into Entity.Fields")
	}
}

func TestEntity_Lookup_NotFound(t *testing.T) {
	e := &Entity{Name: "article", Fields: []Field{{Name: Bare("id")}}}

	if got := e.Lookup("nonexistent"); got != nil {
		t.Errorf("Lookup(\"nonexistent\") = %+v, want nil", got)
	}
}

func TestSchema_Lookup_Found(t *testing.T) {
	s := &Schema{Entities: []Entity{{Name: "article"}, {Name: "user"}}}

	got := s.Lookup("user")

	if got == nil {
		t.Fatal(`Lookup("user") = nil, want the user entity`)
	}
	if got.Name != "user" {
		t.Errorf("Lookup(\"user\").Name = %q, want %q", got.Name, "user")
	}
}

func TestSchema_Lookup_NotFound(t *testing.T) {
	s := &Schema{Entities: []Entity{{Name: "article"}}}

	if got := s.Lookup("nonexistent"); got != nil {
		t.Errorf("Lookup(\"nonexistent\") = %+v, want nil", got)
	}
}

// TestEntity_HasNoImportsField pins the spec §2.2 decision that imports are a
// property of an output file, not an entity: an entity spans four packages
// (model, store, httpapi, hooks) with different import needs, so a single
// Imports field on Entity would be meaningless.
func TestEntity_HasNoImportsField(t *testing.T) {
	typ := reflect.TypeOf(Entity{})
	if _, ok := typ.FieldByName("Imports"); ok {
		t.Error("Entity has an Imports field; imports belong to an output file, per spec §2.2")
	}
}

// TestRelation_HoldsResolvedTarget documents that Relation.Target is a
// resolved *Entity pointer, consistent with SortKey.Field and Filter.Field.
func TestRelation_HoldsResolvedTarget(t *testing.T) {
	target := &Entity{Name: "user"}
	rel := Relation{Name: "author", Target: target}

	if rel.Target != target {
		t.Errorf("Relation.Target = %p, want %p", rel.Target, target)
	}
}
