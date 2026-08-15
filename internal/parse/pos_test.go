package parse

import (
	"testing"

	"github.com/goccy/go-yaml/token"

	"github.com/cdhdt/lapigo/internal/source"
)

func TestSpanOf_QuotedKeyIncludesQuotesInWidth(t *testing.T) {
	tok := &token.Token{
		Value:  "created_at",
		Origin: "\n      \"created_at\"",
		Position: &token.Position{
			Line:   4,
			Column: 7,
		},
	}

	start, end := spanOf(tok)

	want := source.Pos{Line: 4, Column: 7}
	if start != want {
		t.Fatalf("start = %+v, want %+v", start, want)
	}
	wantEnd := source.Pos{Line: 4, Column: 19}
	if end != wantEnd {
		t.Fatalf("end = %+v, want %+v", end, wantEnd)
	}
}

func TestSpanOf_NonASCIIIdentifierCountsRunesNotBytes(t *testing.T) {
	tok := &token.Token{
		Value:  "café",
		Origin: "\n      café",
		Position: &token.Position{
			Line:   5,
			Column: 7,
		},
	}

	start, end := spanOf(tok)

	if start != (source.Pos{Line: 5, Column: 7}) {
		t.Fatalf("start = %+v, want {5 7}", start)
	}
	// "café" is 4 runes (é is one rune, two bytes), so the exclusive end is
	// column 11 -- not 12, which is what counting bytes would produce.
	if end != (source.Pos{Line: 5, Column: 11}) {
		t.Fatalf("end = %+v, want {5 11}", end)
	}
}

func TestAtOf_UsesExplicitValueButTokenSpan(t *testing.T) {
	// A sort key token "-created_at" spans the whole written form (including
	// the direction sign), but the At's Value is the bare field name used to
	// look the field up. atOf must keep those independent: the span comes
	// from the token, the Value from the argument.
	tok := &token.Token{
		Value:  "-created_at",
		Origin: "-created_at",
		Position: &token.Position{
			Line:   12,
			Column: 12,
		},
	}

	at := atOf("created_at", tok)

	if at.Value != "created_at" {
		t.Fatalf("Value = %q, want %q", at.Value, "created_at")
	}
	if at.Pos != (source.Pos{Line: 12, Column: 12}) {
		t.Fatalf("Pos = %+v, want {12 12}", at.Pos)
	}
	if at.End != (source.Pos{Line: 12, Column: 23}) {
		t.Fatalf("End = %+v, want {12 23}", at.End)
	}
}

// TestSpanOf_MultiLineQuotedScalarEndReflectsActualLineWidth is the
// regression test for defect 12: spanOf hardcoded end.Line to the token's
// start line and counted every rune of the trimmed Origin including
// embedded newlines, so a scalar written across two physical lines produced
// an End far past the actual width of its start line. Given a token whose
// written form spans two lines, End.Line must account for the line break,
// and End.Column must not be inflated by counting the second line's runes
// onto the first.
func TestSpanOf_MultiLineQuotedScalarEndReflectsActualLineWidth(t *testing.T) {
	// A double-quoted scalar folded across two source lines:
	//   "line one
	//   line two"
	// Origin (as goccy records it, padding included) starts with the
	// newline/indentation before the token, then the token's own text,
	// itself containing one embedded newline.
	tok := &token.Token{
		Value:  "line one line two",
		Origin: "\n  \"line one\nline two\"",
		Position: &token.Position{
			Line:   4,
			Column: 3,
		},
	}

	start, end := spanOf(tok)

	if start != (source.Pos{Line: 4, Column: 3}) {
		t.Fatalf("start = %+v, want {4 3}", start)
	}
	// The old implementation reported end == {Line: 4, Column: 21}: Column
	// 3 (start) + 18 (every rune of `"line one\nline two"`, the embedded
	// newline included as if it were a printable character) -- claiming a
	// span 18 columns wide on a line the scalar's own first line of text
	// ("line one) is only 9 columns into. The fixed behaviour advances
	// End.Line by the one embedded newline, and measures End.Column from
	// only the portion of raw actually written on the start line
	// (`"line one`, 9 runes: quote, l,i,n,e,space,o,n,e).
	want := source.Pos{Line: 5, Column: 12}
	if end != want {
		t.Fatalf("end = %+v, want %+v", end, want)
	}
}

// TestSpanOf_NilTokenReturnsZeroSpan and
// TestSpanOf_NilPositionReturnsZeroSpan are the regression tests for defect
// 14: spanOf dereferenced tok.Position without a nil guard, while
// errors.go's syntaxErrorDiagnostic already guards the same field before
// calling it -- proof the field is known to be nullable. atOf and addNode
// pass node.GetToken() straight through with no guard of their own.
// CLAUDE.md forbids panics in library code; spanOf must degrade to the
// zero span instead.
func TestSpanOf_NilTokenReturnsZeroSpan(t *testing.T) {
	start, end := spanOf(nil)
	if start != (source.Pos{}) || end != (source.Pos{}) {
		t.Fatalf("spanOf(nil) = (%+v, %+v), want zero spans", start, end)
	}
}

func TestSpanOf_NilPositionReturnsZeroSpan(t *testing.T) {
	tok := &token.Token{Value: "x", Origin: "x", Position: nil}
	start, end := spanOf(tok)
	if start != (source.Pos{}) || end != (source.Pos{}) {
		t.Fatalf("spanOf(token with nil Position) = (%+v, %+v), want zero spans", start, end)
	}
}

func TestSpanOf_PlainScalarTrimsSurroundingWhitespaceFromOrigin(t *testing.T) {
	tok := &token.Token{
		Value:  "plain",
		Origin: " plain\n",
		Position: &token.Position{
			Line:   1,
			Column: 1,
		},
	}

	start, end := spanOf(tok)

	if start != (source.Pos{Line: 1, Column: 1}) {
		t.Fatalf("start = %+v, want {1 1}", start)
	}
	if end != (source.Pos{Line: 1, Column: 6}) {
		t.Fatalf("end = %+v, want {1 6}", end)
	}
}
