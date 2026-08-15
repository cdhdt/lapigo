package diag

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/cdhdt/lapigo/internal/source"
)

// replacementRune stands in for any rune sanitizeLine strips out of a
// rendered source snippet: every control character (unicode.IsControl), the
// BOM, a zero-width or bidi-control rune, and the Unicode line/paragraph
// separators. It is exactly one rune, matching the one input rune it
// replaces one-for-one, so no column downstream of the substitution shifts
// (spec section 4.5).
//
// U+FFFD REPLACEMENT CHARACTER is the standard library's own choice for
// "invalid or unrepresentable" text (utf8.RuneError has the same value) and
// every text-capable terminal renders it as a visible glyph -- unlike, say,
// '?', which a schema could also legitimately contain as real content.
const replacementRune = '\uFFFD'

// bom is the three-byte UTF-8 encoding of U+FEFF, the byte order mark.
const bom = "\xef\xbb\xbf"

// Render turns every diagnostic in ds into the compiler-grade report
// described in spec section 4.1: a header line, the offending source line
// with a right-aligned line number in a gutter shared by the whole report,
// a caret line underlining the exact span, and the hint indented beneath
// it. Diagnostics are rendered in position order -- File, Line, Column,
// EndColumn, Severity, Message; see Diagnostics.sorted -- each block
// separated from the next by one blank line, never in accumulation order
// (spec section 4.5).
//
// srcs supplies the source of every file a diagnostic in ds might name.
// Render looks each diagnostic up by its File field; a diagnostic whose
// File has no matching entry in srcs still renders its header line, with
// the source snippet and caret omitted. There is no fallback to "the"
// source when a name doesn't match: a renderer given a single source used
// to render every diagnostic against it regardless of which file the
// diagnostic actually named, printing a line from a.yaml beneath a header
// that says b.yaml. Confidently wrong output is worse than none (spec
// section 4.5).
//
// The returned string has no trailing newline; blocks are joined with a
// blank line ("\n\n") between them. A caller that prints the report
// directly is responsible for its own trailing newline (fmt.Println, or an
// explicit "\n" before writing) -- Render does not add one, so callers that
// concatenate the result into a larger document don't have to trim it back
// out.
//
// Every rune in a rendered source snippet for which unicode.IsControl
// holds -- plus the BOM, the zero-width/bidi-control block U+200B-U+200F,
// and the Unicode line/paragraph separators U+2028/U+2029 -- is replaced by
// one visible placeholder rune before it reaches the returned string. A
// schema file is untrusted input: fetched from a template, pasted from an
// issue, checked into someone else's repository. Echoing its bytes into a
// terminal verbatim means "\x1b]0;pwned\x07" rewrites the window title,
// "\x08" backspaces over the diagnostic lapigo just printed, and
// "\x1b[2J" clears the screen.
//
// Render never panics on a bad position: a line number past the end of the
// file, a column past the end of a line, a reversed or missing EndColumn,
// an invalid Pos (source.Pos.IsValid), or an empty source all degrade to
// something sensible -- usually: omit the source snippet, or clamp the
// caret -- rather than indexing out of range.
//
// Alignment is exact only for width-1 characters. Pos.Column counts runes,
// which is the right unit for naming a character unambiguously -- a byte
// column is wrong on any line with a multi-byte character -- but a rune is
// not a terminal display cell. "名前" is two runes and four cells; a
// combining mark is a rune occupying no cell of its own; some emoji
// sequences are five runes rendered as one cell. Because the caret is
// placed by counting runes, not cells, it visibly drifts on any line
// containing such characters. Implementing display width is a later phase
// (spec section 4.5); TestRender_WideCharacterCaretIsRuneAlignedNotDisplayAligned
// records the known skew instead of hiding it.
func (ds Diagnostics) Render(srcs ...source.File) string {
	sorted := ds.sorted()

	linesByFile := make(map[string][][]rune, len(srcs))
	for _, f := range srcs {
		linesByFile[f.Name] = splitLines(f.Src)
	}

	// The gutter width is computed once, across every diagnostic that will
	// actually show a snippet, so two diagnostics at lines 9 and 100 in the
	// same report get "     9 |" and "   100 |" -- aligned on the same
	// column -- rather than each picking a width from its own line number
	// and misaligning against its neighbours, which is what per-diagnostic
	// gutter sizing produced.
	gutterWidth := 0
	for _, d := range sorted {
		lines := linesByFile[d.File]
		if !d.Pos.IsValid() || d.Pos.Line < 1 || d.Pos.Line > len(lines) {
			continue
		}
		if w := len(strconv.Itoa(d.Pos.Line)); w > gutterWidth {
			gutterWidth = w
		}
	}

	blocks := make([]string, len(sorted))
	for i, d := range sorted {
		blocks[i] = d.render(linesByFile[d.File], gutterWidth)
	}
	return strings.Join(blocks, "\n\n")
}

// splitLines turns src into one sanitized rune slice per line, ready to
// print in a rendered snippet: O(1) access by line number, rune-indexed
// slicing by column, no control bytes, no lone tab.
//
// A leading byte-order mark is stripped before anything else runs, because
// a real YAML parser strips it too: leaving it in place would make the
// source line built here start one rune to the right of where every Pos on
// line 1 was actually computed, so the caret would underline the BOM
// instead of the character the diagnostic blames.
//
// Lines are split on "\r\n", a lone "\n", or a lone "\r" -- YAML 1.2 treats
// all three as line breaks, goccy follows the spec, and diag's own
// CheckNoTabs scanner (tabs.go) agrees with this splitting so the two never
// disagree about what line N is. strings.Split(src, "\n") followed by
// trimming a trailing '\r' handles CRLF but leaves a lone '\r' sitting
// inside a "line" as a literal character instead of splitting on it --
// wrong for any file edited on classic Mac line endings or hand-assembled
// with a bare '\r'.
//
// A source ending in a line break does not produce a trailing, phantom
// empty line: "a: 1\n" is one line, not two, so a diagnostic can never be
// pointed at the (nonexistent) line after the last real one and render an
// empty row with a stray caret.
func splitLines(src []byte) [][]rune {
	src = stripLeadingBOM(src)
	raw := splitRawLines(string(src))
	out := make([][]rune, len(raw))
	for i, l := range raw {
		out[i] = sanitizeLine(l)
	}
	return out
}

// stripLeadingBOM removes a byte-order mark occupying the first three bytes
// of src, if present. It runs once, on the whole file, before any line
// splitting or column counting. A BOM anywhere else in the file is not a
// leading BOM -- it is suspicious content like any other, and sanitizeLine
// replaces it with the placeholder rune instead.
func stripLeadingBOM(src []byte) []byte {
	if len(src) >= len(bom) && string(src[:len(bom)]) == bom {
		return src[len(bom):]
	}
	return src
}

// splitRawLines splits s on every YAML 1.2 line break -- "\r\n", a lone
// "\n", or a lone "\r" -- and drops the final empty element produced when s
// ends in one of them, so a trailing line break does not manufacture a
// phantom last line.
//
// Scanning byte-by-byte is safe even though s may contain multi-byte UTF-8
// runes: every continuation byte of a multi-byte encoding has its top bit
// set (>= 0x80), so it can never be mistaken for the single-byte ASCII '\n'
// (0x0A) or '\r' (0x0D) this function looks for.
func splitRawLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			lines = append(lines, s[start:i])
			start = i + 1
		case '\r':
			lines = append(lines, s[start:i])
			if i+1 < len(s) && s[i+1] == '\n' {
				i++ // "\r\n" is one line break, not two
			}
			start = i + 1
		}
	}
	lines = append(lines, s[start:])

	if s != "" && start == len(s) && len(lines) > 1 {
		// The last break found landed exactly at the end of s, so the
		// final append above added the phantom "" this trims.
		lines = lines[:len(lines)-1]
	}
	return lines
}

// sanitizeLine converts every tab in s to a single space, then replaces any
// remaining control character or other invisible/suspicious rune with the
// single visible replacementRune -- one rune in, one rune out, so no column
// computed against the original line ever needs adjusting.
//
// Tab gets its own case rather than falling into the general control-
// character branch (unicode.IsControl('\t') is true) because tabs are
// already refused outright by CheckNoTabs with their own diagnostic (spec
// section 4.2); swapping a tab for a literal space rather than the
// placeholder rune keeps every column computed against a tab-containing
// line correct while fixing the *visual* misalignment a raw tab would
// otherwise cause (a terminal's tab stops are not fixed at one column, so
// printing it raw would shift everything after it out from under the caret
// it's meant to sit under). Deleting this substitution changes nothing else
// in the package -- it is the entire reason splitLines exists as its own
// function rather than a one-line strings.Split.
func sanitizeLine(s string) []rune {
	runes := []rune(s)
	out := make([]rune, len(runes))
	for i, r := range runes {
		switch {
		case r == '\t':
			out[i] = ' '
		case unicode.IsControl(r) || isSuspiciousRune(r):
			out[i] = replacementRune
		default:
			out[i] = r
		}
	}
	return out
}

// isSuspiciousRune reports whether r is invisible or format-control in a way
// unicode.IsControl does not already catch. IsControl only covers the
// Unicode Cc (control) category -- it is false for the BOM and for
// U+200B-U+200F (Cf, format) and U+2028/U+2029 (Zl/Zp, separator) -- yet all
// of these can hide or rewrite what a terminal displays just as effectively
// as a C0 control byte can.
func isSuspiciousRune(r rune) bool {
	switch {
	case r == '\uFEFF': // byte order mark
		return true
	case r >= '\u200B' && r <= '\u200F': // zero-width space .. right-to-left mark
		return true
	case r == '\u2028' || r == '\u2029': // line separator, paragraph separator
		return true
	default:
		return false
	}
}

// render builds the full multi-line block for one diagnostic: header, then
// (if a source line is available) the source and caret rows, then (if set)
// the hint rows. gutterWidth is the digit width of the largest line number
// in the whole report (see Render), shared by every block so gutters line
// up across diagnostics on different lines.
func (d Diagnostic) render(lines [][]rune, gutterWidth int) string {
	var b strings.Builder
	b.WriteString(d.header())

	if d.Pos.IsValid() && d.Pos.Line >= 1 && d.Pos.Line <= len(lines) {
		line := lines[d.Pos.Line-1]
		lineLen := len(line)

		// Clamp the caret span into a range that can always be drawn: start
		// is a valid rune index in [1, lineLen+1] (lineLen+1 meaning "right
		// after the last rune", for a diagnostic that blames end-of-line).
		// end is forced to be at least start+1 so the span always has
		// non-zero width, even when EndColumn was unset, equal to Pos.Column,
		// or reversed -- and bounded relative to the line so a garbage
		// EndColumn cannot produce an unbounded run of carets.
		start := clampInt(d.Pos.Column, 1, lineLen+1)
		end := clampInt(d.EndColumn, start+1, lineLen+2)
		width := end - start

		numStr := strconv.Itoa(d.Pos.Line)
		// gutterWidth is computed by Render as the maximum digit width over
		// exactly the diagnostics that reach this point (same
		// IsValid/in-range filter as above), so it can never be smaller
		// than len(numStr) here; pad is never negative.
		pad := gutterWidth - len(numStr)
		gutter := "   " + strings.Repeat(" ", pad) + numStr + " |"
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
