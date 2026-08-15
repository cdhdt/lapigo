package diag_test

import (
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestRender_NeverPanics exercises every malformed-input case called out in
// the package's build brief: a position past the end of the line, past the
// end of the file, an invalid Pos, an empty source, a file with no trailing
// newline, CRLF line endings, and an end column before the start column.
// None of these may panic; each must render *something* rather than crash
// the caller mid-validation.
func TestRender_NeverPanics(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		d    diag.Diagnostic
	}{
		{
			name: "zero Pos",
			src:  []byte("sort: [-id]\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Message: "no position to blame"},
		},
		{
			name: "line past end of file",
			src:  []byte("only one line\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 99, Column: 1}, Message: "boom"},
		},
		{
			name: "line number zero",
			src:  []byte("only one line\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 0, Column: 3}, Message: "boom"},
		},
		{
			name: "negative line",
			src:  []byte("only one line\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: -5, Column: 3}, Message: "boom"},
		},
		{
			name: "column past end of line",
			src:  []byte("short\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 500}, EndColumn: 510, Message: "boom"},
		},
		{
			name: "negative column",
			src:  []byte("short\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: -3}, EndColumn: 2, Message: "boom"},
		},
		{
			name: "empty source, line 1",
			src:  []byte{},
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
		},
		{
			name: "nil source",
			src:  nil,
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, Message: "boom"},
		},
		{
			name: "no trailing newline, points at last line",
			src:  []byte("first\nsecond line no newline"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, EndColumn: 6, Message: "boom"},
		},
		{
			name: "CRLF line endings",
			src:  []byte("first\r\nsecond\r\nthird\r\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, EndColumn: 6, Message: "boom"},
		},
		{
			name: "lone CR line endings",
			src:  []byte("first\rsecond\rthird\r"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, EndColumn: 6, Message: "boom"},
		},
		{
			name: "leading BOM",
			src:  append([]byte("\xef\xbb\xbf"), []byte("only line\n")...),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "boom"},
		},
		{
			name: "control bytes in source",
			src:  []byte("a\x1b[2Jb\x00c\x08d\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 1}, EndColumn: 2, Message: "boom"},
		},
		{
			name: "end column equal to start column",
			src:  []byte("sort: [-id]\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, EndColumn: 8, Message: "boom"},
		},
		{
			name: "end column before start column",
			src:  []byte("sort: [-id]\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, EndColumn: 3, Message: "boom"},
		},
		{
			name: "end column absurdly large",
			src:  []byte("sort: [-id]\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, EndColumn: 1 << 20, Message: "boom"},
		},
		{
			name: "end column unset (zero)",
			src:  []byte("sort: [-id]\n"),
			d:    diag.Diagnostic{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, Message: "boom"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Render panicked: %v", r)
				}
			}()
			ds := diag.Diagnostics{tc.d}
			got := ds.Render(source.File{Name: "lapigo.yaml", Src: tc.src})
			if got == "" {
				t.Fatalf("Render() returned empty string for a non-empty Diagnostics")
			}
			if !strings.Contains(got, "lapigo.yaml") {
				t.Fatalf("Render() = %q, want it to still contain the file name", got)
			}
		})
	}
}

// TestRender_CaretNeverRunsAwayOnAnAbsurdEndColumn is the regression test
// for the mutant an adversarial review found surviving: replacing the
// lineLen+2 caret-span clamp with something enormous (e.g. 1<<20) passed
// every prior test, because the only assertion touching the caret line was
// "contains at least one ^". This pins the exact caret width for a garbage
// EndColumn: it must be clamped to the line, not to the requested span.
func TestRender_CaretNeverRunsAwayOnAnAbsurdEndColumn(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("sort: [-id]\n")} // 11 runes long
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, EndColumn: 1 << 20, Message: "boom"},
	}
	got := ds.Render(src)
	want := "lapigo.yaml:1:8: boom\n" +
		"   1 | sort: [-id]\n" +
		"     |        ^^^^^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q (caret must clamp to the line, not draw a million carets)", got, want)
	}
}

// TestRender_ZeroPosOmitsLineCol confirms the zero Pos degrades to a header
// with no ":line:col:" segment, per the doc comment on Diagnostic.Pos: the
// zero Pos means "no position", and printing ":0:0:" would look like a real
// — and wrong — location rather than the absence of one.
func TestRender_ZeroPosOmitsLineCol(t *testing.T) {
	ds := diag.Diagnostics{{File: "lapigo.yaml", Message: "synthesised value"}}
	got := ds.Render(source.File{Name: "lapigo.yaml", Src: []byte("irrelevant\n")})
	want := "lapigo.yaml: synthesised value"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

// TestRender_InvalidNonZeroPosAlsoOmitsLineCol is the regression test for
// the defect where the guard used Pos.IsZero() instead of Pos.IsValid():
// {Line: 0, Column: 5} is not the zero Pos, yet it is exactly as meaningless
// as one, and used to render the misleading header "lapigo.yaml:0:5:". A
// negative line is equally meaningless and used to render
// "lapigo.yaml:-3:-9:". Both must degrade the same way the zero Pos does.
func TestRender_InvalidNonZeroPosAlsoOmitsLineCol(t *testing.T) {
	cases := []struct {
		name string
		pos  source.Pos
	}{
		{"line zero, column set", source.Pos{Line: 0, Column: 5}},
		{"negative line and column", source.Pos{Line: -3, Column: -9}},
		{"valid line, column zero", source.Pos{Line: 4, Column: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ds := diag.Diagnostics{{File: "lapigo.yaml", Pos: tc.pos, Message: "boom"}}
			got := ds.Render(source.File{Name: "lapigo.yaml", Src: []byte("a: 1\nb: 2\nc: 3\nd: 4\ne: 5\n")})
			want := "lapigo.yaml: boom"
			if got != want {
				t.Fatalf("Render() = %q, want %q (an invalid, non-zero Pos must degrade exactly like the zero Pos)", got, want)
			}
		})
	}
}

// TestRender_CRLFDoesNotShowStrayCarriageReturn confirms a CRLF source line
// is displayed without the trailing '\r' leaking into the printed snippet
// (which would otherwise show as a stray control character or misalign the
// caret against what the user actually sees in their editor).
func TestRender_CRLFDoesNotShowStrayCarriageReturn(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("first\r\nsecond\r\nthird\r\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, EndColumn: 7, Message: "boom"},
	}
	got := ds.Render(src)
	if strings.Contains(got, "\r") {
		t.Fatalf("Render() output contains a stray \\r:\n%q", got)
	}
	want := "lapigo.yaml:2:1: boom\n" +
		"   2 | second\n" +
		"     | ^^^^^^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}

// TestRender_EndColumnBeforeStartStillDrawsACaret confirms a reversed span
// degrades to a minimal, visible caret rather than silently drawing
// nothing (which would produce a confusing report: a message with a source
// line but no indication of what part of it is at fault).
func TestRender_EndColumnBeforeStartStillDrawsACaret(t *testing.T) {
	src := source.File{Name: "lapigo.yaml", Src: []byte("sort: [-id]\n")}
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, EndColumn: 1, Message: "boom"},
	}
	got := ds.Render(src)
	want := "lapigo.yaml:1:8: boom\n" +
		"   1 | sort: [-id]\n" +
		"     |        ^"
	if got != want {
		t.Fatalf("Render() =\n%q\nwant\n%q", got, want)
	}
}
