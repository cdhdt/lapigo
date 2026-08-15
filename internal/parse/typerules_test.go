package parse

import "testing"

// TestBuildField_EnumWithNoValuesIsRejected is the regression test for
// defect 7: `type: enum` with no `values:` set EnumGoType only inside the
// "values" case of buildField's switch, so an enum field that never wrote
// `values:` left EnumGoType at its zero value "" -- Field.GoType() then
// answers "*" (Nullable, the default) or "" (not nullable) for a field the
// schema calls an enum, with zero diagnostics and an empty CHECK
// constraint downstream.
func TestBuildField_EnumWithNoValuesIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      status: { type: enum }\n")
	want := "field `status` has type `enum` but no `values`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildField_EnumWithEmptyValuesIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      status: { type: enum, values: [] }\n")
	want := "field `status` has type `enum` but no `values`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestBuildField_ValuesOnNonEnumFieldIsRejected is defect 8's first case:
// `values:` on a `type: string` field silently set EnumGoType on a field
// that will never use it.
func TestBuildField_ValuesOnNonEnumFieldIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title: { type: string, values: [a, b] }\n")
	want := "key \"values\" is not valid on a field of type \"string\""
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestBuildField_MaxOnNonStringFieldIsRejected is defect 8's second case,
// the type half.
func TestBuildField_MaxOnNonStringFieldIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      count: { type: int, max: 10 }\n")
	want := "key \"max\" is not valid on a field of type \"int\""
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestBuildField_NegativeMaxIsRejected is defect 8's second case, the range
// half: `max: -1` became `varchar(-1)` with zero diagnostics. This
// supersedes and inverts TestIntNodeValue_NegativeAccepted, which actively
// blessed the bug (CLAUDE.md: "invariants live in code, not in comments" --
// a test that asserts the bug is worse than no test).
func TestBuildField_NegativeMaxIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title: { type: string, max: -1 }\n")
	want := "field `title` `max` must be a positive integer, found -1"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestBuildField_ZeroMaxIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title: { type: string, max: 0 }\n")
	want := "field `title` `max` must be a positive integer, found 0"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestBuildField_PositiveMaxOnStringFieldStillAccepted guards against an
// overcorrection: a valid `max:` on a `type: string` field must still
// parse cleanly.
func TestBuildField_PositiveMaxOnStringFieldStillAccepted(t *testing.T) {
	schema := parseOK(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title: { type: string, max: 200 }\n")
	got := schema.Entities[0].Fields[1].Max
	if got == nil || *got != 200 {
		t.Fatalf("Max = %v, want 200", got)
	}
}

// TestRelationKeys_MaxIsRejected is the regression test for defect 9:
// relationKeys whitelisted "max", "values", "default" and "version" for a
// belongsTo field, but buildRelationField's switch never reads any of
// them, so checkUnknownKeys let them through and buildRelationField
// silently dropped them -- `version: true` on a belongsTo in particular
// means the optimistic-concurrency column silently does not exist.
func TestRelationKeys_MaxIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      author: { type: belongsTo, target: article, max: 10 }\n")
	want := "unknown key \"max\" in field `author`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestRelationKeys_ValuesIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      author: { type: belongsTo, target: article, values: [a] }\n")
	want := "unknown key \"values\" in field `author`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestRelationKeys_DefaultIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      author: { type: belongsTo, target: article, default: now }\n")
	want := "unknown key \"default\" in field `author`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestRelationKeys_VersionIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      author: { type: belongsTo, target: article, version: true }\n")
	want := "unknown key \"version\" in field `author`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestNodeTypeName_BlockScalarNamedDistinctlyFromString is the regression
// test for the Tier 7 quality item: nodeTypeName mapped a block scalar
// (`|` or `>`) to "a string", the same label requireString's own error
// uses for the thing it wanted -- so `type: |\n  foo` produced the
// self-contradictory "must be a string, found a string" instead of naming
// what was actually there.
func TestNodeTypeName_BlockScalarNamedDistinctlyFromString(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      title:\n        type: |\n          not actually a flow string\n")
	want := "field `title` `type` must be a string, found a block scalar"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}
