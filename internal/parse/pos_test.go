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
