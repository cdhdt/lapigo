package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestResolveVersion_DuplicateIsRejectedWithPosition is the regression test
// for defect 11: spec §3.1 says "at most one [version field] per entity",
// but nothing in this package checked it before this test existed, so a
// schema declaring two fell through to ir.Schema.Freeze's self-check --
// which reports "internal: lapigo produced an invalid schema; this is a bug
// in lapigo, not your schema file", with no position, for a mistake the
// user actually made. Two fields marked `version: true` must be rejected
// here, each at its own span, exactly as resolvePK rejects two `pk: true`
// fields.
func TestResolveVersion_DuplicateIsRejectedWithPosition(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true }\n" +
		"      a: { type: int, version: true }\n" +
		"      b: { type: int, version: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	if len(diags) != 2 {
		t.Fatalf("diags = %+v, want 2 (one per offending field, like two_pk)", diags)
	}
	want := `entity "article" has 2 fields marked ` + "`version: true`" + `, want at most 1`
	for i, d := range diags {
		if d.Message != want {
			t.Errorf("diags[%d].Message = %q, want %q", i, d.Message, want)
		}
		if !d.Pos.IsValid() {
			t.Errorf("diags[%d].Pos is not valid; want a position on the offending field, not a bug report with none", i)
		}
	}
	// Positions must be distinct -- each offending field blamed at its own
	// span, not both diagnostics pointing at the same place.
	if diags[0].Pos == diags[1].Pos {
		t.Errorf("both diagnostics share Pos %+v, want each field's own position", diags[0].Pos)
	}
	for _, d := range diags {
		if d.Message == "internal: lapigo produced an invalid schema; this is a bug in lapigo, not your schema file: ir: entity \"article\" has 2 fields marked Version, want at most 1" {
			t.Fatalf("duplicate version fell through to Freeze's internal-bug diagnostic instead of being caught in parse: %+v", diags)
		}
	}
}

// TestResolveVersion_WrongTypeIsRejectedWithPosition is the regression test
// for issue #46: `version: { type: text, version: true }` used to validate
// with zero diagnostics on develop, even though step 7's `SET version =
// version + 1` cannot run against a text column. Confirmed against develop
// before this test was written to assert anything: Parse produced a non-nil
// schema and zero diagnostics for this exact source.
func TestResolveVersion_WrongTypeIsRejectedWithPosition(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true }\n" +
		"      v: { type: text, required: true, version: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want 1", diags)
	}
	d := diags[0]
	wantMsg := `entity "article"'s version field "v" has type "text", want ` + "`int` or `bigint`"
	wantHint := "mark `v` `type: int` or `type: bigint`; an optimistic-concurrency counter must be an integer the store can increment"
	if d.Message != wantMsg {
		t.Errorf("Message = %q, want %q", d.Message, wantMsg)
	}
	if d.Hint != wantHint {
		t.Errorf("Hint = %q, want %q", d.Hint, wantHint)
	}
	if !d.Pos.IsValid() {
		t.Errorf("Pos is not valid; want a position on the offending field")
	}
}

// TestResolveVersion_JSONTypeIsRejectedWithPosition reproduces issue #46's
// second trap example verbatim: `version: { type: json, version: true }`
// also validated with zero diagnostics on develop.
func TestResolveVersion_JSONTypeIsRejectedWithPosition(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true }\n" +
		"      v: { type: json, required: true, version: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want 1", diags)
	}
	d := diags[0]
	wantMsg := `entity "article"'s version field "v" has type "json", want ` + "`int` or `bigint`"
	if d.Message != wantMsg {
		t.Errorf("Message = %q, want %q", d.Message, wantMsg)
	}
}

// TestResolveVersion_NullableIsRejectedWithPosition is the regression test
// for issue #46 rule 2: a version field that is not `required: true` is
// nullable (internal/parse/field.go: `f.Nullable = !required && !pk`), and a
// NULL version makes every `version = $n` comparison yield unknown, so an
// `If-Match` update matches zero rows and returns 409 forever -- spec §3.3
// rule 3's reasoning applied to this column. `version: { type: int, version:
// true }` with no `required:` used to validate with zero diagnostics.
func TestResolveVersion_NullableIsRejectedWithPosition(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true }\n" +
		"      v: { type: int, version: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want 1", diags)
	}
	d := diags[0]
	wantMsg := `entity "article"'s version field "v" is nullable`
	wantHint := "mark `v` `required: true`; a NULL version makes every `version = $n` comparison yield unknown, " +
		"so an `If-Match` update matches zero rows and returns 409 forever (spec §3.3 rule 3's reasoning, applied to this column)"
	if d.Message != wantMsg {
		t.Errorf("Message = %q, want %q", d.Message, wantMsg)
	}
	if d.Hint != wantHint {
		t.Errorf("Hint = %q, want %q", d.Hint, wantHint)
	}
	if !d.Pos.IsValid() {
		t.Errorf("Pos is not valid; want a position on the offending field")
	}
}

// TestResolveVersion_PromotesEntityVersion is the regression test for issue
// #25's second gap: Entity.Version must be set to the same *Field as the
// one field marked `version: true`, mirroring how resolvePK sets Entity.PK
// (TestParse_MinimalSchema's own e.PK assertion), so a §6.6 consumer never
// has to rescan Fields to find the optimistic-concurrency column. Before
// this test existed, resolveVersion validated the field but never promoted
// it, so a valid schema parsed with zero diagnostics and Entity.Version
// left nil.
func TestResolveVersion_PromotesEntityVersion(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true }\n" +
		"      v: { type: int, required: true, version: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if schema == nil {
		t.Fatal("schema is nil")
	}
	e := schema.Entities[0]
	vField := e.Lookup("v")
	if vField == nil {
		t.Fatal(`Lookup("v") = nil`)
	}
	if e.Version != vField {
		t.Error("Entity.Version does not point at the same *Field as the field marked version: true")
	}
	if err := schema.Freeze(); err != nil {
		t.Errorf("Freeze() = %v, want nil", err)
	}
}

// TestResolveVersion_NoVersionFieldLeavesEntityVersionNil proves the
// converse: an entity with no `version: true` field at all must leave
// Entity.Version nil, not some stale or default-zero *Field -- Version is
// optional (spec §3.1's "at most one"), unlike PK.
func TestResolveVersion_NoVersionFieldLeavesEntityVersionNil(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	e := schema.Entities[0]
	if e.Version != nil {
		t.Errorf("Entity.Version = %+v, want nil: no field marked version: true", e.Version)
	}
	if err := schema.Freeze(); err != nil {
		t.Errorf("Freeze() = %v, want nil", err)
	}
}

// TestResolveVersion_PKIsRejectedWithPosition is the regression test for
// issue #46 rule 3 and its own trap example: `id: { type: uuid, pk: true,
// version: true }` used to validate with zero diagnostics on develop. A
// field that is both the primary key and the version column fails two
// independent rules at once here -- uuid is not int/bigint, and it may not
// also be the PK -- so this pins both diagnostics, in the order the three
// checks run (type, then nullable, then PK).
func TestResolveVersion_PKIsRejectedWithPosition(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true, version: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	if len(diags) != 2 {
		t.Fatalf("diags = %+v, want 2 (type and PK, in that order)", diags)
	}

	wantMsg0 := `entity "article"'s version field "id" has type "uuid", want ` + "`int` or `bigint`"
	if diags[0].Message != wantMsg0 {
		t.Errorf("diags[0].Message = %q, want %q", diags[0].Message, wantMsg0)
	}

	wantMsg1 := `entity "article"'s version field "id" may not also be the primary key`
	wantHint1 := "mark `pk: true` on a different field, or remove `version: true` from `id`"
	if diags[1].Message != wantMsg1 {
		t.Errorf("diags[1].Message = %q, want %q", diags[1].Message, wantMsg1)
	}
	if diags[1].Hint != wantHint1 {
		t.Errorf("diags[1].Hint = %q, want %q", diags[1].Hint, wantHint1)
	}
	for i, d := range diags {
		if !d.Pos.IsValid() {
			t.Errorf("diags[%d].Pos is not valid; want a position on the offending field", i)
		}
	}
}
