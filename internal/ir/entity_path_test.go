package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestEntity_Path covers the collection and single-resource paths for every
// EndpointKind on an ordinary "id"-keyed entity (issue #47): the collection
// path is the table name for List and Create, and the single-resource path
// appends a wildcard for Get, Update and Delete.
func TestEntity_Path(t *testing.T) {
	pk := &Field{Name: source.Bare("id"), GoName: "ID", Column: "id", Type: FieldTypeUUID, PK: true}
	e := &Entity{Name: "article", Table: "articles", Fields: []*Field{pk}, PK: pk}

	tests := []struct {
		kind EndpointKind
		want string
	}{
		{EndpointList, "/articles"},
		{EndpointGet, "/articles/{id}"},
		{EndpointCreate, "/articles"},
		{EndpointUpdate, "/articles/{id}"},
		{EndpointDelete, "/articles/{id}"},
	}
	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			if got := e.Path(tt.kind); got != tt.want {
				t.Errorf("Path(%s) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}

// TestEntity_Path_WildcardNamedAfterPK proves the single-resource path's
// wildcard is named after Entity.PK.Column, not hardcoded to "{id}": an
// entity whose primary key column is "slug" must yield "/things/{slug}",
// never "/things/{id}" -- a handler that decoded "{id}" but bound the value
// to a "slug" column would be generated code that lies to the person who
// owns it (issue #47).
func TestEntity_Path_WildcardNamedAfterPK(t *testing.T) {
	pk := &Field{Name: source.Bare("slug"), GoName: "Slug", Column: "slug", Type: FieldTypeString, PK: true}
	e := &Entity{Name: "thing", Table: "things", Fields: []*Field{pk}, PK: pk}

	tests := []struct {
		kind EndpointKind
		want string
	}{
		{EndpointList, "/things"},
		{EndpointGet, "/things/{slug}"},
		{EndpointCreate, "/things"},
		{EndpointUpdate, "/things/{slug}"},
		{EndpointDelete, "/things/{slug}"},
	}
	for _, tt := range tests {
		t.Run(tt.kind.String(), func(t *testing.T) {
			if got := e.Path(tt.kind); got != tt.want {
				t.Errorf("Path(%s) = %q, want %q", tt.kind, got, tt.want)
			}
		})
	}
}
