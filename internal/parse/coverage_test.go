package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// parseOK parses src and fails the test if there are any diagnostics.
func parseOK(t *testing.T, src string) *ir.Schema {
	t.Helper()
	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if schema == nil {
		t.Fatal("schema is nil")
	}
	return schema
}

// firstMessage parses src and returns the Message of the first diagnostic,
// failing the test if there is not exactly one.
func firstMessage(t *testing.T, src string) string {
	t.Helper()
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	return diags[0].Message
}

func TestBuildField_TargetOnDeleteRejectedOnNonRelationField(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true, target: user }\n")
	want := `key "target" is not valid on a field of type "uuid"`
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildDefault_UUIDKeyword(t *testing.T) {
	schema := parseOK(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true, default: uuid }\n")
	got := schema.Entities[0].Fields[0].Default
	if got == nil || got.Kind != ir.DefaultUUID {
		t.Fatalf("Default = %+v, want Kind=DefaultUUID", got)
	}
}

func TestBuildDefault_IntegerLiteral(t *testing.T) {
	schema := parseOK(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      count: { type: int, default: 42 }\n")
	got := schema.Entities[0].Fields[1].Default
	if got == nil || got.Kind != ir.DefaultLiteral || got.Literal != "42" {
		t.Fatalf("Default = %+v, want {DefaultLiteral 42}", got)
	}
}

func TestBuildDefault_NonScalarIsDiagnostic(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true, default: [1, 2] }\n")
	want := "field `id` `default` must be a scalar, found a sequence"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestRequireString_WrongTypeReported(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: 123, pk: true }\n")
	want := "field `id` `type` must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestRequireBool_WrongTypeReported(t *testing.T) {
	// pk: "yes" fails the boolean check, so no field ends up marked pk:
	// true -- both diagnostics are legitimate, and the test asserts on the
	// one requireBool itself produces, in raw accumulation order (the
	// wrong-type diagnostic is appended while building the field, before
	// resolvePK's own "no pk" diagnostic runs).
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(
		"entities:\n  article:\n    fields:\n      id: { type: uuid, pk: \"yes\" }\n")})
	if len(diags) != 2 {
		t.Fatalf("diags = %+v, want 2", diags)
	}
	want := "field `id` `pk` must be a boolean (true or false), found a string"
	if diags[0].Message != want {
		t.Fatalf("diags[0].Message = %q, want %q", diags[0].Message, want)
	}
}

func TestRequireBool_NullTypeReported(t *testing.T) {
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(
		"entities:\n  article:\n    fields:\n      id: { type: uuid, pk: null }\n")})
	if len(diags) != 2 {
		t.Fatalf("diags = %+v, want 2", diags)
	}
	want := "field `id` `pk` must be a boolean (true or false), found null"
	if diags[0].Message != want {
		t.Fatalf("diags[0].Message = %q, want %q", diags[0].Message, want)
	}
}

func TestRequireInt_FloatTypeReported(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title: { type: string, max: 3.5 }\n")
	want := "field `title` `max` must be an integer, found a float"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestIntNodeValue_NegativeAccepted(t *testing.T) {
	schema := parseOK(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title: { type: string, max: -1 }\n")
	got := schema.Entities[0].Fields[1].Max
	if got == nil || *got != -1 {
		t.Fatalf("Max = %v, want -1", got)
	}
}

func TestIntNodeValue_OutOfRangeIsDiagnostic(t *testing.T) {
	// 18e18 fits uint64 (max ~1.8e19), so it still parses as an
	// *ast.IntegerNode -- but exceeds math.MaxInt on a 64-bit platform
	// (~9.2e18), exercising intNodeValue's own overflow check rather than
	// requireInt's node-type check a truly unparseable literal would hit
	// instead.
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title: { type: string, max: 18000000000000000000 }\n")
	want := "field `title` `max` is out of range"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestSplitSortSign_ExplicitPlusIsAscending(t *testing.T) {
	name, desc := splitSortSign("+created_at")
	if name != "created_at" || desc {
		t.Fatalf("splitSortSign(+created_at) = (%q, %v), want (created_at, false)", name, desc)
	}
}

func TestBuildRelationField_TargetWrongType(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      author: { type: belongsTo, target: 123 }\n")
	want := "field `author` `target` must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildRelationField_OnDeleteWrongType(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      author: { type: belongsTo, target: article, on_delete: 123 }\n")
	want := "field `author` `on_delete` must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildRelationField_RequiredMakesColumnNotNull(t *testing.T) {
	schema := parseOK(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      author: { type: belongsTo, target: article, required: true }\n")
	author := schema.Entities[0].Fields[1]
	if author.Nullable {
		t.Error("Nullable = true, want false (required: true)")
	}
	if len(schema.Entities[0].Relations) != 1 || schema.Entities[0].Relations[0].Nullable {
		t.Errorf("Relations = %+v, want Nullable=false", schema.Entities[0].Relations)
	}
}

func TestParse_StripsLeadingBOM(t *testing.T) {
	src := "\xef\xbb\xbfentities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n"
	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if schema.Entities[0].Name != "article" {
		t.Fatalf("Name = %q, want %q", schema.Entities[0].Name, "article")
	}
	idField := schema.Entities[0].Fields[0]
	if idField.Name.Pos != (source.Pos{Line: 4, Column: 7}) {
		t.Fatalf("Name.Pos = %+v, want {4 7} (BOM must not shift line-1 columns downstream)", idField.Name.Pos)
	}
}

func TestEntries_NonStringKeyIsDiagnostic(t *testing.T) {
	// A second, valid entity keeps the non-string-key entity from also
	// tripping "`entities` has no members" once its own bad-keyed entry is
	// dropped, isolating the diagnostic this test actually cares about.
	msg := firstMessage(t, "entities:\n  123:\n    fields:\n      id: { type: uuid, pk: true }\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n")
	want := "map key must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildEnumValues_NonStringEntryIsDiagnostic(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      status: { type: enum, values: [draft, 5] }\n")
	want := "field `status` `values` entry must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildPendingFilters_NonStringEntryIsDiagnostic(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n    filters: [3]\n")
	want := "entity `article` `filters` entry must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildEndpoints_NotASequenceIsDiagnostic(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n    endpoints: 5\n")
	want := "`endpoints` must be a sequence, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildEndpoints_NonStringEntryIsDiagnostic(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n    endpoints: [3]\n")
	want := "`endpoints` entry must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildSortSpec_NonStringEntryIsDiagnostic(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n    sort: [3]\n")
	want := "entity `article` `sort` entry must be a string, found an integer"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}
