package diag_test

import (
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/source"
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
// spec's header and its own diagram disagree by one. source.Pos's contract
// (1-based, exact) leaves no room for an intentional off-by-one, and baking
// one into Render would misplace every caret produced from a real,
// correctly computed source.Pos elsewhere in the codebase. This test therefore
// asserts column 12, matching the diagram's actual caret position, and
// keeps everything else — gutter layout, caret width, hint indentation —
// byte-for-byte as specified.
func TestRender_SpecExample(t *testing.T) {
	src := []byte(strings.Repeat("\n", 11) + "    sort: [-created_at]\n")
	ds := diag.Diagnostics{
		{
			File:      "lapigo.yaml",
			Pos:       source.Pos{Line: 12, Column: 12},
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

	got := ds.Render(source.File{Name: "lapigo.yaml", Src: src})
	if got != want {
		t.Fatalf("Render() =\n%s\nwant\n%s", got, want)
	}
}

// TestRender_TwoDiagnosticsOnSameLine is one of the two exact-golden tests
// the package existed to pass but never had: two diagnostics blaming
// different spans on the *same* line. This exercises the column tiebreak in
// sorted (never exercised by any prior test — every earlier sort test used
// distinct lines) and pins the full two-block report byte for byte, so a
// mutation to gutter/caret math, block separation, or ordering fails a
// concrete string comparison instead of a Contains/HasPrefix check.
func TestRender_TwoDiagnosticsOnSameLine(t *testing.T) {
	src := []byte("entities: {article: 1, user: 2}\n")
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 12}, EndColumn: 19, Message: `entity "article" must be a mapping`},
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 24}, EndColumn: 28, Message: `entity "user" must be a mapping`},
	}

	got := ds.Render(source.File{Name: "lapigo.yaml", Src: src})
	want := "lapigo.yaml:1:12: entity \"article\" must be a mapping\n" +
		"   1 | entities: {article: 1, user: 2}\n" +
		"     |            ^^^^^^^\n" +
		"\n" +
		"lapigo.yaml:1:24: entity \"user\" must be a mapping\n" +
		"   1 | entities: {article: 1, user: 2}\n" +
		"     |                        ^^^^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_TabDiagnosticAgainstItsOwnSource is the other exact-golden
// test the package existed to pass: the actual output of CheckNoTabs,
// rendered against the very tab-containing source it scanned. This pins the
// tab→space substitution in splitLines (deleting it previously failed
// nothing — the only prior test asserted a Contains on the header) and the
// tab diagnostic's absolute EndColumn (the prior test only asserted
// EndColumn == Pos.Column+1, relationally, which passes even if both sides
// are wrong by the same amount).
func TestRender_TabDiagnosticAgainstItsOwnSource(t *testing.T) {
	src := []byte("entities:\n\tarticle:\n")
	ds := diag.CheckNoTabs("lapigo.yaml", src)

	got := ds.Render(source.File{Name: "lapigo.yaml", Src: src})
	want := "lapigo.yaml:2:1: tab character is not allowed in schema files\n" +
		"   2 |  article:\n" +
		"     | ^\n" +
		"   use spaces instead of tabs; YAML forbids tabs for indentation, and " +
		"lapigo forbids them everywhere else so reported column numbers stay correct"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_MultiFileLooksUpEachDiagnosticByItsOwnFile is the regression
// test for the blocking defect: Render used to take one src and render
// every diagnostic against it regardless of Diagnostic.File, so a
// diagnostic naming b.yaml printed a line from a.yaml beneath its header.
// Render must look each diagnostic up by File and show the *right* file's
// line under each header.
func TestRender_MultiFileLooksUpEachDiagnosticByItsOwnFile(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "a.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 4, Message: "problem in a"},
		{File: "b.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 4, Message: "problem in b"},
	}
	got := ds.Render(
		source.File{Name: "a.yaml", Src: []byte("aaa: 1\n")},
		source.File{Name: "b.yaml", Src: []byte("bbb: 2\n")},
	)
	want := "a.yaml:1:1: problem in a\n" +
		"   1 | aaa: 1\n" +
		"     | ^^^\n" +
		"\n" +
		"b.yaml:1:1: problem in b\n" +
		"   1 | bbb: 2\n" +
		"     | ^^^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_MissingFileOmitsSnippetButKeepsHeader confirms the other half
// of the multi-file guarantee: a diagnostic naming a file Render was not
// given still renders — its header line, with no source snippet — rather
// than silently dropping the diagnostic or panicking.
func TestRender_MissingFileOmitsSnippetButKeepsHeader(t *testing.T) {
	ds := diag.Diagnostics{
		{File: "missing.yaml", Pos: source.Pos{Line: 3, Column: 5}, Message: "boom"},
	}
	got := ds.Render(source.File{Name: "other.yaml", Src: []byte("irrelevant\n")})
	want := "missing.yaml:3:5: boom"
	if got != want {
		t.Fatalf("Render() = %q, want %q (header only, no snippet)", got, want)
	}
}

func TestRender_MultipleDiagnosticsSortedAndSeparated(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("line one\nline two\nline three\n")}
	// Appended out of position order on purpose: Render must sort, not rely
	// on accumulation order.
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 3, Column: 1}, EndColumn: 2, Message: "third"},
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "first"},
		{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 6}, EndColumn: 9, Message: "second"},
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
	src := source.File{Name: "lapigo.yaml", Src: []byte(line + "\n")}

	// "bad" starts at rune column 7 (c=1,a=2,f=3,é=4,:=5,' '=6,b=7).
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 7}, EndColumn: 10, Message: "bad identifier"},
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

// TestRender_WideCharacterCaretIsRuneAlignedNotDisplayAligned records the
// documented limitation from Render's doc comment (spec §4.5): the caret is
// placed by rune count, not by terminal display width, so it visibly drifts
// on any line with wide characters. "名前" is two runes but four display
// cells; "bad" starts at rune column 6 but would sit at display column 8 in
// a real terminal. This is not a bug to fix — display width is a later
// phase — but the skew must be recorded, not silently hidden.
func TestRender_WideCharacterCaretIsRuneAlignedNotDisplayAligned(t *testing.T) {
	line := "名前: bad" // 名, 前 are one rune / three display cells each in most terminals
	src := source.File{Name: "lapigo.yaml", Src: []byte(line + "\n")}

	// Rune columns: 名=1 前=2 :=3 ' '=4 b=5 a=6 d=7. "bad" starts at rune
	// column 5, though a display-width-aware renderer would place it at
	// display column 9 (each wide rune costs 2 cells: 1+1+2+2=... the exact
	// number is not the point; the point is that rune column 5 undercounts
	// it).
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 5}, EndColumn: 8, Message: "bad identifier"},
	}

	got := ds.Render(src)
	want := "lapigo.yaml:1:5: bad identifier\n" +
		"   1 | 名前: bad\n" +
		"     |     ^^^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q (rune-aligned, not display-aligned: the caret sits under \"bad\" by rune count, four columns short of where a display-width-aware renderer would put it)", got, want)
	}
}

func TestRender_WarningSeverityPrefix(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("sort: [-updated_at]\n")}
	ds := diag.Diagnostics{
		{
			Severity: diag.Warning,
			File:     "lapigo.yaml",
			Pos:      source.Pos{Line: 1, Column: 9},
			Message:  "sort key \"updated_at\" is mutable",
		},
	}
	got := ds.Render(src)
	if !strings.HasPrefix(got, "lapigo.yaml:1:9: warning: sort key \"updated_at\" is mutable") {
		t.Fatalf("Render() = %q, want a \"warning: \" prefix on the message", got)
	}
}

func TestRender_ErrorSeverityHasNoPrefix(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("sort: [-updated_at]\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 9}, Message: "boom"},
	}
	got := ds.Render(src)
	if !strings.HasPrefix(got, "lapigo.yaml:1:9: boom") {
		t.Fatalf("Render() = %q, want no severity prefix for a plain error", got)
	}
}

func TestRender_NoHintOmitsHintLines(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("sort: [-id]\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, EndColumn: 11, Message: "boom"},
	}
	got := ds.Render(src)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("Render() with no hint produced %d lines, want exactly 3 (header, source, caret):\n%q", len(lines), got)
	}
}

// TestRender_GutterWidthIsSharedAcrossTheWholeReport is the regression test
// for the defect where gutter width was computed per diagnostic: lines 9
// and 100 in one report used to produce "   9 |" and "   100 |" — misaligned
// with each other. rustc and go both compute the gutter width once, across
// the whole report; this pins the exact aligned output.
func TestRender_GutterWidthIsSharedAcrossTheWholeReport(t *testing.T) {
	var src strings.Builder
	for i := 1; i <= 100; i++ {
		src.WriteString("line\n")
	}
	f := source.File{Name: "lapigo.yaml", Src: []byte(src.String())}

	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 9, Column: 1}, EndColumn: 2, Message: "at nine"},
		{File: "lapigo.yaml", Pos: source.Pos{Line: 100, Column: 1}, EndColumn: 2, Message: "at hundred"},
	}
	got := ds.Render(f)
	want := "lapigo.yaml:9:1: at nine\n" +
		"     9 | line\n" +
		"       | ^\n" +
		"\n" +
		"lapigo.yaml:100:1: at hundred\n" +
		"   100 | line\n" +
		"       | ^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_PhantomTrailingLineIsTrimmed confirms a source ending in a
// newline does not manufacture a nonexistent last line: "a: 1\n" is one
// line, so a diagnostic on line 2 finds no source and renders header-only,
// rather than an empty row with a stray caret and trailing whitespace.
func TestRender_PhantomTrailingLineIsTrimmed(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("a: 1\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, Message: "no such line"},
	}
	got := ds.Render(src)
	want := "lapigo.yaml:2:1: no such line"
	if got != want {
		t.Fatalf("Render() = %q, want %q (line 2 must not exist for a one-line, newline-terminated file)", got, want)
	}
}

// TestRender_LoneCarriageReturnIsALineBreak confirms YAML 1.2's line-break
// rule (a lone '\r' ends a line, same as '\n' or '\r\n') is honoured: a
// three-line file written with bare '\r' line endings must render as three
// lines, with line 2 addressable and shown correctly.
func TestRender_LoneCarriageReturnIsALineBreak(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("a: 1\rb: 2\rc: 3\r")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, EndColumn: 2, Message: "problem on line two"},
	}
	got := ds.Render(src)
	want := "lapigo.yaml:2:1: problem on line two\n" +
		"   2 | b: 2\n" +
		"     | ^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_LeadingBOMIsStrippedNotUnderlined confirms a leading byte-order
// mark does not occupy rune column 1: with the BOM stripped (as a real
// parser strips it), Pos{Line:1, Column:1} must underline 'a', the first
// real character, not the BOM.
func TestRender_LeadingBOMIsStrippedNotUnderlined(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: append([]byte("\xef\xbb\xbf"), []byte("a: 1\n")...)}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "boom"},
	}
	got := ds.Render(src)
	want := "lapigo.yaml:1:1: boom\n" +
		"   1 | a: 1\n" +
		"     | ^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q (BOM must not shift the line or occupy a column)", got, want)
	}
}

// --- Control-byte sanitization ---------------------------------------------

// TestRender_ControlBytesAreSanitized is the regression test for the
// blocking security defect: a schema file is untrusted input, and control
// bytes echoed into a rendered snippet can rewrite the terminal's window
// title (ESC ]0;...BEL), overwrite already-printed text (backspace), or
// clear the screen (ESC [2J). None of the raw bytes may reach Render's
// output, and the substitution must be one rune for one rune so surrounding
// column positions are unaffected.
func TestRender_ControlBytesAreSanitized(t *testing.T) {
	// "key1: \x1b]0;pwned\x07 key2: \x08\x00 end" — an ESC, a BEL, a
	// backspace and a NUL embedded in otherwise-normal content.
	src := source.File{Name: "lapigo.yaml", Src: []byte("key: a\x1b]0;pwned\x07b\x08c\x00d\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 4, Message: "boom"},
	}
	got := ds.Render(src)

	for _, bad := range []string{"\x1b", "\x00", "\x08", "\x07"} {
		if strings.Contains(got, bad) {
			t.Fatalf("Render() output contains raw control byte %q:\n%q", bad, got)
		}
	}

	want := "lapigo.yaml:1:1: boom\n" +
		"   1 | key: a\ufffd]0;pwned\ufffdb\ufffdc\ufffdd\n" +
		"     | ^^^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_ControlByteSubstitutionPreservesColumns confirms the placement
// promise: replacing a control rune with the placeholder must not shift any
// column after it. A caret positioned after the sanitized run must still
// land on the correct character.
func TestRender_ControlByteSubstitutionPreservesColumns(t *testing.T) {
	// "a" + ESC + "bad" — ESC is one rune at column 2; "bad" starts at
	// column 3 both before and after sanitization, since substitution is
	// one rune for one rune.
	src := source.File{Name: "lapigo.yaml", Src: []byte("a\x1bbad\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 3}, EndColumn: 6, Message: "bad identifier"},
	}
	got := ds.Render(src)
	want := "lapigo.yaml:1:3: bad identifier\n" +
		"   1 | a\ufffdbad\n" +
		"     |   ^^^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_ZeroWidthAndSeparatorRunesAreSanitized covers the invisible
// characters unicode.IsControl does not catch on its own (it only covers
// the Cc category): the BOM appearing mid-file, the zero-width space
// U+200B, and the Unicode line separator U+2028. Each must become the
// placeholder rune, one for one.
func TestRender_ZeroWidthAndSeparatorRunesAreSanitized(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("a\ufeffb\u200bc\u2028d\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "boom"},
	}
	got := ds.Render(src)
	want := "lapigo.yaml:1:1: boom\n" +
		"   1 | a\ufffdb\ufffdc\ufffdd\n" +
		"     | ^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}
