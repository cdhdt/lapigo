package diag_test

import (
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
)

// TestRender_SpecExample reproduces the worked example from
// docs/superpowers/specs/2026-08-15-phase1-core-design.md §4.1 and
// CLAUDE.md's "compiler-grade error messages" section: gutter alignment, a
// caret span matching the offending token's width, and a hint indented
// under the gutter.
//
// One deliberate deviation from the spec's literal text: the spec's example
// prints column 11 in the header ("lapigo.yaml:12:11: ..."), but its own
// source line ("    sort: [-created_at]", 4-space indent, verified
// character-for-character below) and its own caret row place the caret
// under the '-' of "-created_at", which is rune column 12, not 11 — the
// spec's header and its own diagram disagree by one. ir.Pos's contract
// (1-based, exact) leaves no room for an intentional off-by-one, and baking
// one into Render would misplace every caret produced from a real,
// correctly computed ir.Pos elsewhere in the codebase. This test therefore
// asserts column 12, matching the diagram's actual caret position, and
// keeps everything else — gutter layout, caret width, hint indentation —
// byte-for-byte as specified.
func TestRender_SpecExample(t *testing.T) {
	src := []byte(strings.Repeat("\n", 11) + "    sort: [-created_at]\n")
	ds := diag.Diagnostics{
		{
			File:      "lapigo.yaml",
			Pos:       ir.Pos{Line: 12, Column: 12},
			EndColumn: 23, // "-created_at" is 11 runes: 12..22 inclusive
			Message:   `sort key "created_at" is not unique`,
			Hint: "the last sort key must be unique; add a second key such as `-id`,\n" +
				"or mark `created_at` unique",
		},
	}

	want := "lapigo.yaml:12:12: sort key \"created_at\" is not unique\n" +
		"   12 |     sort: [-created_at]\n" +
		"      |            ^^^^^^^^^^^\n" +
		"   the last sort key must be unique; add a second key such as `-id`,\n" +
		"   or mark `created_at` unique"

	got := ds.Render(src)
	if got != want {
		t.Fatalf("Render() =\n%s\nwant\n%s", got, want)
	}
}

func TestRender_MultipleDiagnosticsSortedAndSeparated(t *testing.T) {
	src := []byte("line one\nline two\nline three\n")
	// Appended out of position order on purpose: Render must sort, not rely
	// on accumulation order.
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: ir.Pos{Line: 3, Column: 1}, EndColumn: 2, Message: "third"},
		{File: "lapigo.yaml", Pos: ir.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "first"},
		{File: "lapigo.yaml", Pos: ir.Pos{Line: 2, Column: 6}, EndColumn: 9, Message: "second"},
	}

	got := ds.Render(src)
	blocks := strings.Split(got, "\n\n")
	if len(blocks) != 3 {
		t.Fatalf("Render() produced %d blocks (split on blank line), want 3:\n%s", len(blocks), got)
	}
	if !strings.HasPrefix(blocks[0], "lapigo.yaml:1:1: first") {
		t.Errorf("block 0 = %q, want it to start with the line-1 diagnostic", blocks[0])
	}
	if !strings.HasPrefix(blocks[1], "lapigo.yaml:2:6: second") {
		t.Errorf("block 1 = %q, want it to start with the line-2 diagnostic", blocks[1])
	}
	if !strings.HasPrefix(blocks[2], "lapigo.yaml:3:1: third") {
		t.Errorf("block 2 = %q, want it to start with the line-3 diagnostic", blocks[2])
	}

	// Render must not mutate accumulation order as a side effect.
	if ds[0].Message != "third" || ds[1].Message != "first" || ds[2].Message != "second" {
		t.Fatalf("Render() mutated ds's order: %+v", ds)
	}
}

// TestRender_NonASCIIRuneColumn is the trap test the package exists to
// pass: a caret computed in bytes lands in the wrong place on a line
// containing a multi-byte character. "café" has a 2-byte, 1-rune 'é'; the
// offending identifier "bad" starts right after it. A byte-based
// implementation would place the caret one column too far right.
func TestRender_NonASCIIRuneColumn(t *testing.T) {
	line := "café: bad" // é is one rune, two UTF-8 bytes
	src := []byte(line + "\n")

	// "bad" starts at rune column 7 (c=1,a=2,f=3,é=4,:=5,' '=6,b=7).
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: ir.Pos{Line: 1, Column: 7}, EndColumn: 10, Message: "bad identifier"},
	}

	got := ds.Render(src)
	lines := strings.Split(got, "\n")
	if len(lines) < 3 {
		t.Fatalf("Render() produced %d lines, want at least 3:\n%s", len(lines), got)
	}
	caretLine := lines[2]

	wantCaretLine := "     |       ^^^"
	if caretLine != wantCaretLine {
		t.Fatalf("caret line = %q, want %q (byte-based indexing would shift this right by one column)", caretLine, wantCaretLine)
	}
}

func TestRender_WarningSeverityPrefix(t *testing.T) {
	src := []byte("sort: [-updated_at]\n")
	ds := diag.Diagnostics{
		{
			Severity: diag.Warning,
			File:     "lapigo.yaml",
			Pos:      ir.Pos{Line: 1, Column: 9},
			Message:  "sort key \"updated_at\" is mutable",
		},
	}
	got := ds.Render(src)
	if !strings.HasPrefix(got, "lapigo.yaml:1:9: warning: sort key \"updated_at\" is mutable") {
		t.Fatalf("Render() = %q, want a \"warning: \" prefix on the message", got)
	}
}

func TestRender_ErrorSeverityHasNoPrefix(t *testing.T) {
	src := []byte("sort: [-updated_at]\n")
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: ir.Pos{Line: 1, Column: 9}, Message: "boom"},
	}
	got := ds.Render(src)
	if !strings.HasPrefix(got, "lapigo.yaml:1:9: boom") {
		t.Fatalf("Render() = %q, want no severity prefix for a plain error", got)
	}
}

func TestRender_NoHintOmitsHintLines(t *testing.T) {
	src := []byte("sort: [-id]\n")
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: ir.Pos{Line: 1, Column: 8}, EndColumn: 11, Message: "boom"},
	}
	got := ds.Render(src)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("Render() with no hint produced %d lines, want exactly 3 (header, source, caret):\n%q", len(lines), got)
	}
}
