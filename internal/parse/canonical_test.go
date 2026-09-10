package parse

import (
	"os"
	"reflect"
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestParse_CanonicalSchema parses the spec §3 canonical schema (article,
// with a belongsTo to a second "user" entity added so the target actually
// resolves -- the spec's own code block shows only "article", illustrating
// the field format rather than a complete, self-consistent file) and
// asserts the fully resolved IR, field by field, against every field the
// spec's Entity/Field/Relation/Endpoint shapes define.
func TestParse_CanonicalSchema(t *testing.T) {
	src, err := os.ReadFile("testdata/canonical.yaml")
	if err != nil {
		t.Fatal(err)
	}

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: src})

	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if schema == nil {
		t.Fatal("schema is nil")
	}
	if err := schema.Freeze(); err != nil {
		t.Fatalf("Freeze() = %v, want nil", err)
	}

	stripPositions(schema)

	article := &ir.Entity{
		Name:   "article",
		GoName: "Article",
		Table:  "articles",
		Fields: []*ir.Field{
			{Name: source.Bare("id"), GoName: "ID", Column: "id", Type: ir.FieldTypeUUID, PK: true},
			{Name: source.Bare("title"), GoName: "Title", Column: "title", Type: ir.FieldTypeString, Max: intPtr(200)},
			{Name: source.Bare("body"), GoName: "Body", Column: "body", Type: ir.FieldTypeText, Nullable: true},
			{
				Name: source.Bare("status"), GoName: "Status", Column: "status", Type: ir.FieldTypeEnum,
				EnumGoType: "ArticleStatus",
				EnumValues: []ir.EnumValue{{Name: source.Bare("draft"), GoName: "Draft"}, {Name: source.Bare("published"), GoName: "Published"}},
			},
			{Name: source.Bare("slug"), GoName: "Slug", Column: "slug", Type: ir.FieldTypeString, Nullable: true, Unique: true, ReadOnly: true},
			{Name: source.Bare("author"), GoName: "AuthorID", Column: "author_id", Type: ir.FieldTypeUUID, Nullable: true},
			{
				Name: source.Bare("created_at"), GoName: "CreatedAt", Column: "created_at", Type: ir.FieldTypeTimestamp,
				Immutable: true, Default: &ir.DefaultValue{Kind: ir.DefaultNow},
			},
			{Name: source.Bare("version"), GoName: "Version", Column: "version", Type: ir.FieldTypeInt, Version: true},
		},
		Sort: ir.SortSpec{Desc: true},
		Endpoints: []ir.Endpoint{
			{Kind: ir.EndpointList, Path: "/articles"},
			{Kind: ir.EndpointGet, Path: "/articles/{id}"},
			{Kind: ir.EndpointCreate, Path: "/articles"},
			{Kind: ir.EndpointUpdate, Path: "/articles/{id}"},
			{Kind: ir.EndpointDelete, Path: "/articles/{id}"},
		},
	}
	article.PK = article.Fields[0]
	article.Sort.Keys = []ir.SortKey{{Field: article.Fields[6]}, {Field: article.Fields[0]}}
	article.Filters = []ir.Filter{{Field: article.Fields[3], Op: ir.FilterOpEq}, {Field: article.Fields[5], Op: ir.FilterOpEq}}

	user := &ir.Entity{
		Name:   "user",
		GoName: "User",
		Table:  "users",
		Fields: []*ir.Field{
			{Name: source.Bare("id"), GoName: "ID", Column: "id", Type: ir.FieldTypeUUID, PK: true},
			{Name: source.Bare("email"), GoName: "Email", Column: "email", Type: ir.FieldTypeString, Unique: true},
		},
		Endpoints: []ir.Endpoint{
			{Kind: ir.EndpointList, Path: "/users"},
			{Kind: ir.EndpointGet, Path: "/users/{id}"},
			{Kind: ir.EndpointCreate, Path: "/users"},
			{Kind: ir.EndpointUpdate, Path: "/users/{id}"},
			{Kind: ir.EndpointDelete, Path: "/users/{id}"},
		},
	}
	user.PK = user.Fields[0]
	// "user" writes no `sort:` at all -- canonical.yaml deliberately leaves
	// it out to exercise the parser's own default (spec §3.3): omitting
	// `sort:` synthesizes `[-<pk>]`, since the PK is unique by construction.
	user.Sort = ir.SortSpec{Desc: true, Keys: []ir.SortKey{{Field: user.Fields[0]}}}

	article.Relations = []ir.Relation{
		{Name: "author", GoName: "Author", Target: user, Column: "author_id", GoType: "pgtype.UUID", Nullable: true, OnDelete: "RESTRICT"},
	}

	want := &ir.Schema{Entities: []*ir.Entity{article, user}}

	if !reflect.DeepEqual(schema, want) {
		for i, e := range schema.Entities {
			we := want.Entities[i]
			for j, f := range e.Fields {
				wf := we.Fields[j]
				if !reflect.DeepEqual(f, wf) {
					t.Errorf("entity %s field %d mismatch:\n got: %#v\nwant: %#v", e.Name, j, f, wf)
				}
			}
			if !reflect.DeepEqual(e.Relations, we.Relations) {
				t.Errorf("entity %s relations mismatch:\n got: %#v\nwant: %#v", e.Name, e.Relations, we.Relations)
			}
		}
		t.Fatalf("schema mismatch.\n got: %s\nwant: %s", dumpSchema(schema), dumpSchema(want))
	}
}

func intPtr(v int) *int { return &v }

func dumpSchema(s *ir.Schema) string {
	if s == nil {
		return "<nil>"
	}
	out := ""
	for _, e := range s.Entities {
		out += "\nentity " + e.Name + ":\n"
		for _, f := range e.Fields {
			out += "  field " + f.Name.Value + " = " + dumpField(f) + "\n"
		}
		out += "  PK=" + dumpFieldRef(e.PK) + "\n"
		out += "  Sort=" + dumpSort(e.Sort) + "\n"
		for _, filt := range e.Filters {
			out += "  Filter " + dumpFieldRef(filt.Field) + "\n"
		}
		for _, rel := range e.Relations {
			out += "  Relation " + rel.Name + " -> " + rel.Target.Name + "\n"
		}
		for _, ep := range e.Endpoints {
			out += "  Endpoint " + ep.Kind.String() + " " + ep.Path + "\n"
		}
	}
	return out
}

func dumpField(f *ir.Field) string {
	if f == nil {
		return "<nil>"
	}
	return f.GoName + " " + f.Column + " " + f.Type.String()
}

func dumpFieldRef(f *ir.Field) string {
	if f == nil {
		return "<nil>"
	}
	return f.Name.Value
}

func dumpSort(s ir.SortSpec) string {
	out := s.Direction() + ":"
	for _, k := range s.Keys {
		out += " " + dumpFieldRef(k.Field)
	}
	return out
}
