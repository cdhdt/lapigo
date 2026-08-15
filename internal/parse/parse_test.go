package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

func TestParse_RejectsTabsBeforeParsing(t *testing.T) {
	f := source.File{Name: "lapigo.yaml", Src: []byte("entities:\n\tarticle:\n")}

	schema, diags := Parse(f)

	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.Message != "tab character is not allowed in schema files" {
		t.Fatalf("Message = %q", d.Message)
	}
	if d.Pos != (source.Pos{Line: 2, Column: 1}) {
		t.Fatalf("Pos = %+v, want {2 1}", d.Pos)
	}
}

func TestParse_MinimalSchema(t *testing.T) {
	src := "entities:\n" +
		"  article:\n" +
		"    fields:\n" +
		"      id: { type: uuid, pk: true }\n"
	f := source.File{Name: "lapigo.yaml", Src: []byte(src)}

	schema, diags := Parse(f)

	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if schema == nil {
		t.Fatal("schema is nil")
	}
	if len(schema.Entities) != 1 {
		t.Fatalf("len(Entities) = %d, want 1", len(schema.Entities))
	}
	e := schema.Entities[0]
	if e.Name != "article" {
		t.Errorf("Name = %q, want %q", e.Name, "article")
	}
	if e.GoName != "Article" {
		t.Errorf("GoName = %q, want %q", e.GoName, "Article")
	}
	if e.Table != "articles" {
		t.Errorf("Table = %q, want %q", e.Table, "articles")
	}
	if len(e.Fields) != 1 {
		t.Fatalf("len(Fields) = %d, want 1", len(e.Fields))
	}
	idField := e.Fields[0]
	if idField.Name.Value != "id" {
		t.Errorf("Name.Value = %q, want %q", idField.Name.Value, "id")
	}
	if idField.Name.Pos != (source.Pos{Line: 4, Column: 7}) {
		t.Errorf("Name.Pos = %+v, want {4 7}", idField.Name.Pos)
	}
	if idField.Name.End != (source.Pos{Line: 4, Column: 9}) {
		t.Errorf("Name.End = %+v, want {4 9}", idField.Name.End)
	}
	if idField.GoName != "ID" {
		t.Errorf("GoName = %q, want %q", idField.GoName, "ID")
	}
	if idField.Column != "id" {
		t.Errorf("Column = %q, want %q", idField.Column, "id")
	}
	if idField.Type != ir.FieldTypeUUID {
		t.Errorf("Type = %v, want FieldTypeUUID", idField.Type)
	}
	if idField.Nullable {
		t.Error("Nullable = true, want false (pk forces NOT NULL)")
	}
	if !idField.PK {
		t.Error("PK = false, want true")
	}
	if e.PK != idField {
		t.Error("Entity.PK does not point at the same *Field as Entity.Fields[0]")
	}
	if err := schema.Freeze(); err != nil {
		t.Errorf("Freeze() = %v, want nil", err)
	}
}

func TestParse_ConvertsSyntaxErrorToDiagnostic(t *testing.T) {
	f := source.File{Name: "lapigo.yaml", Src: []byte("entities:\n  article:\n    fields: [a, b\n")}

	schema, diags := Parse(f)

	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.File != "lapigo.yaml" {
		t.Fatalf("File = %q", d.File)
	}
	if d.Message != "sequence end token ']' not found" {
		t.Fatalf("Message = %q", d.Message)
	}
	if d.Pos != (source.Pos{Line: 3, Column: 13}) {
		t.Fatalf("Pos = %+v, want {3 13}", d.Pos)
	}
	if d.Severity != diag.Error {
		t.Fatalf("Severity = %v, want Error", d.Severity)
	}
}
