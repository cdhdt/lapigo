package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestParse_ImplicitNullValueSpanStaysWithinLine is the regression test for
// defect 13: an entity written with no value at all --
//
//	entities:
//	  article:
//
// -- makes goccy synthesize an *ast.NullNode for "article"'s value, whose
// token is fabricated as if the literal text " null" had actually been
// written right after the colon. spanOf trusted that fabricated width,
// producing a diagnostic at column 12 with EndColumn 16 on a line
// ("  article:") that is only 10 columns wide -- a caret hanging off past
// the last real character on the line.
//
// goccy does distinguish this case from a schema that actually writes
// `article: null`: the fabricated token's Type is token.ImplicitNullType,
// not token.NullType (verified directly against goccy v1.19.2). The fix
// clamps an implicit null's span to the position right after the
// preceding token (the mapping's own `:`) -- always real, always within
// the line -- rather than trusting the fabricated width.
func TestParse_ImplicitNullValueSpanStaysWithinLine(t *testing.T) {
	src := "entities:\n  article:\n"
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})

	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	d := diags[0]
	want := "entity `article` must be a mapping, found null"
	if d.Message != want {
		t.Fatalf("Message = %q, want %q", d.Message, want)
	}
	// Line 2 is "  article:", 10 runes wide; its ':' is column 10, so
	// column 11 -- one past the ':' -- is the last position genuinely
	// within the line. Both the fabricated implicit-null token's old
	// start (column 12) and its old end (column 16) fell past that.
	if d.Pos != (source.Pos{Line: 2, Column: 11}) {
		t.Errorf("Pos = %+v, want {2 11} (right after the entity's ':', clamped from the fabricated column 12)", d.Pos)
	}
	if d.EndColumn != 11 {
		t.Errorf("EndColumn = %d, want 11 (a zero-width point, not the fabricated column 16)", d.EndColumn)
	}
}
