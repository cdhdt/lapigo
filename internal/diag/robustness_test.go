package diag_test

import (
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestRender_NeverPanics exercises every malformed-input case called out in
// the package's build brief: a position past the end of the line, past the
// end of the file, a zero Pos, an empty source, a file with no trailing
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
			got := ds.Render(tc.src)
			if got == "" {
				t.Fatalf("Render() returned empty string for a non-empty Diagnostics")
			}
			if !strings.Contains(got, "lapigo.yaml") {
				t.Fatalf("Render() = %q, want it to still contain the file name", got)
			}
		})
	}
}

// TestRender_ZeroPosOmitsLineCol confirms the zero Pos degrades to a header
// with no ":line:col:" segment, per the doc comment on Diagnostic.Pos: the
// zero Pos means "no position", and printing ":0:0:" would look like a real
// — and wrong — location rather than the absence of one.
func TestRender_ZeroPosOmitsLineCol(t *testing.T) {
	ds := diag.Diagnostics{{File: "lapigo.yaml", Message: "synthesised value"}}
	got := ds.Render([]byte("irrelevant\n"))
	want := "lapigo.yaml: synthesised value"
	if got != want {
		t.Fatalf("Render() = %q, want %q", got, want)
	}
}

// TestRender_CRLFDoesNotShowStrayCarriageReturn confirms a CRLF source line
// is displayed without the trailing '\r' leaking into the printed snippet
// (which would otherwise show as a stray control character or misalign the
// caret against what the user actually sees in their editor).
func TestRender_CRLFDoesNotShowStrayCarriageReturn(t *testing.T) {
	src := []byte("first\r\nsecond\r\nthird\r\n")
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 2, Column: 1}, EndColumn: 7, Message: "boom"},
	}
	got := ds.Render(src)
	if strings.Contains(got, "\r") {
		t.Fatalf("Render() output contains a stray \\r:\n%q", got)
	}
	if !strings.Contains(got, "second") {
		t.Fatalf("Render() = %q, want it to show the second line's text", got)
	}
}

// TestRender_EndColumnBeforeStartStillDrawsACaret confirms a reversed span
// degrades to a minimal, visible caret rather than silently drawing
// nothing (which would produce a confusing report: a message with a source
// line but no indication of what part of it is at fault).
func TestRender_EndColumnBeforeStartStillDrawsACaret(t *testing.T) {
	src := []byte("sort: [-id]\n")
	ds := diag.Diagnostics{
		{File: "lapigo.yaml", Pos: source.Pos{Line: 1, Column: 8}, EndColumn: 1, Message: "boom"},
	}
	got := ds.Render(src)
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("Render() produced %d lines, want 3:\n%q", len(lines), got)
	}
	if !strings.Contains(lines[2], "^") {
		t.Fatalf("caret line = %q, want at least one caret", lines[2])
	}
}
