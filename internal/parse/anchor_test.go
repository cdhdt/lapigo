package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestParse_AnchorIsNamedPlainlyNotAsUnexpectedType is the Tier 7
// regression test for the anchor/alias diagnostic quality issue: an
// anchored mapping is genuinely a mapping (wrapped in an *ast.AnchorNode),
// so rejecting it with "must be a mapping, found a value of an unexpected
// type" was actively wrong about what was there. lapigo does not resolve
// anchors/aliases across a schema; the diagnostic now says so plainly
// instead.
func TestParse_AnchorIsNamedPlainlyNotAsUnexpectedType(t *testing.T) {
	src := "entities:\n  article: &anchor\n    fields:\n      id: { type: uuid, pk: true }\n  other: *anchor\n"
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})

	found := false
	for _, d := range diags {
		if d.Message == "entity `article` must be a mapping, found an anchor (not supported in a lapigo schema)" {
			found = true
		}
		if d.Message == "map key must be a string, found a value of an unexpected type" {
			t.Errorf("still falling through to the generic 'unexpected type' message: %+v", d)
		}
	}
	if !found {
		t.Fatalf("no plain anchor diagnostic among: %+v", diags)
	}
}

// TestParse_MergeKeyIsRejectedWithOneDiagnostic is the Tier 7 regression
// test for the merge-key cascade: `<<: *defaults` used to cascade into
// three diagnostics for one construct. nodeTypeName now names a
// MergeKeyType key plainly, so entries' own "map key must be a string"
// check reports it once and moves on, rather than the merge key, its
// merged-in value, and whatever tried to consume the result each producing
// their own diagnostic.
func TestParse_MergeKeyIsRejectedWithOneDiagnostic(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n" +
		"      id: { type: uuid, pk: true }\n" +
		"      title:\n        <<: *missing\n        type: string\n"
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})

	found := false
	for _, d := range diags {
		if d.Message == "map key must be a string, found a merge key (not supported in a lapigo schema)" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no plain merge-key diagnostic among: %+v", diags)
	}
}
