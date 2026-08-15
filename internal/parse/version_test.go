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
