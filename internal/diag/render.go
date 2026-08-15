package diag

import (
	"strconv"
	"strings"
)

// Render turns every diagnostic in ds into the compiler-grade report
// described in spec §4.1: a header line, the offending source line with a
// right-aligned line number in a gutter, a caret line underlining the exact
// span, and the hint indented beneath it. Diagnostics are rendered in
// position order (line, then column), each block separated from the next by
// one blank line — never in accumulation order, per spec §4.4.
//
// src is the exact bytes of the schema file every diagnostic in ds refers
// to. Phase 1 processes exactly one schema file, so a single src serves the
// whole accumulator; there is no per-diagnostic source lookup. A future
// multi-file phase would need a different signature — not a concern this
// package's callers have yet.
//
// Render never panics on a bad position: a line number past the end of the
// file, a column past the end of a line, a reversed or missing EndColumn,
// the zero Pos, or an empty source all degrade to something sensible
// (usually: omit the source snippet, or clamp the caret) rather than
// indexing out of range.
func (ds Diagnostics) Render(src []byte) string {
	sorted := ds.sorted()
	lines := splitLines(src)

	blocks := make([]string, len(sorted))
	for i, d := range sorted {
		blocks[i] = d.render(lines)
	}
	return strings.Join(blocks, "\n\n")
}

// splitLines breaks src into one rune slice per line, for O(1) access by
// line number and rune-indexed slicing by column. Handles both LF and CRLF
// input by trimming a trailing '\r', and a missing final newline (the last
// "line" from strings.Split still comes out correctly).
//
// Every tab is replaced with a single space. Column arithmetic already
// treats a tab as exactly one rune (spec §4.2: lapigo rejects tabs outright
// specifically so this package never needs a rune-to-display-column map),
// so swapping it for one space rune preserves every column computed against
// this line. What a literal tab would break is the *visual* alignment of
// the caret line underneath it: a terminal's tab stops are not fixed at one
// column, so printing the raw tab would misalign the very column the tab
// diagnostic (CheckNoTabs) is trying to point at.
func splitLines(src []byte) [][]rune {
	raw := strings.Split(string(src), "\n")
	out := make([][]rune, len(raw))
	for i, l := range raw {
		l = strings.TrimSuffix(l, "\r")
		l = strings.ReplaceAll(l, "\t", " ")
		out[i] = []rune(l)
	}
	return out
}

// render builds the full multi-line block for one diagnostic: header, then
// (if a source line is available) the source and caret rows, then (if set)
// the hint rows.
func (d Diagnostic) render(lines [][]rune) string {
	var b strings.Builder
	b.WriteString(d.header())

	if !d.Pos.IsZero() && d.Pos.Line >= 1 && d.Pos.Line <= len(lines) {
		line := lines[d.Pos.Line-1]
		lineLen := len(line)

		// Clamp the caret span into a range that can always be drawn: start
		// is a valid rune index in [1, lineLen+1] (lineLen+1 meaning "right
		// after the last rune", for a diagnostic that blames end-of-line).
		// end is forced to be at least start+1 so the span always has
		// non-zero width, even when EndColumn was unset, equal to Pos.Column,
		// or reversed — and bounded relative to the line so a garbage
		// EndColumn cannot produce an unbounded run of carets.
		start := clampInt(d.Pos.Column, 1, lineLen+1)
		end := clampInt(d.EndColumn, start+1, lineLen+2)
		width := end - start

		numStr := strconv.Itoa(d.Pos.Line)
		gutter := "   " + numStr + " |"
		blankGutter := strings.Repeat(" ", len(gutter)-1) + "|"

		b.WriteByte('\n')
		b.WriteString(gutter)
		b.WriteByte(' ')
		b.WriteString(string(line))

		b.WriteByte('\n')
		b.WriteString(blankGutter)
		b.WriteByte(' ')
		b.WriteString(strings.Repeat(" ", start-1))
		b.WriteString(strings.Repeat("^", width))
	}

	if d.Hint != "" {
		for _, hl := range strings.Split(d.Hint, "\n") {
			b.WriteString("\n   ")
			b.WriteString(hl)
		}
	}

	return b.String()
}

// clampInt confines v to [lo, hi]. Used to keep caret math in range without
// ever indexing a slice out of bounds.
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
