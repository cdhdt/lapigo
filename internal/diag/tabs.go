package diag

import (
	"fmt"
	"unicode/utf8"

	"github.com/cdhdt/lapigo/internal/source"
)

// maxTabDiagnostics caps how many per-line tab diagnostics CheckNoTabs
// returns before collapsing the rest into one summary diagnostic, in the
// spirit of go/scanner's own habit of truncating a long error list rather
// than printing all of it. Without a cap, a 500-line tab-indented file
// (every generated-code sample pasted with a tab-using editor, for example)
// produces one diagnostic per offending line, each carrying the same
// multi-sentence hint, which turns a single mistake into a report over a
// hundred kilobytes long -- unreadable, and no more useful past the first
// few lines than a "your file uses tabs everywhere" summary would be.
const maxTabDiagnostics = 10

// CheckNoTabs scans src for tab characters and returns at most one
// Diagnostic per offending *line*, before any YAML parsing happens. A line
// with several tabs gets one diagnostic whose message states the count
// ("3 tab characters..."), not three diagnostics repeating the same hint. If
// more than maxTabDiagnostics lines contain a tab, only the first
// maxTabDiagnostics get their own diagnostic; a final summary diagnostic
// with no position reports how many more lines were affected, so the
// returned Diagnostics -- and therefore Render's output -- stays bounded
// regardless of how tab-riddled the file is.
//
// Why this exists, and why it runs before parsing rather than being folded
// into a validator downstream (spec §4.2): goccy does not advance its
// column counter across a tab character in flow context, under-counting by
// one per tab. No amount of correct column arithmetic at render time can
// recover from a wrong column at the source. YAML already forbids tabs for
// indentation; lapigo forbids them everywhere in the file, which removes
// the bug class entirely and means Render never needs a
// rune-index-to-display-column map to compensate for tab expansion.
//
// Positions are counted in runes, matching source.Pos: a tab following a
// multi-byte identifier is reported at its rune column, not its byte
// offset. Lines are delimited the same way splitLines (render.go) delimits
// them -- "\r\n", a lone "\n", or a lone "\r" -- so a file using classic Mac
// or bare-CR line endings gets the same line numbers here as it will from
// Render and from the real YAML parser, which follows the YAML 1.2 spec's
// definition of a line break. A leading byte-order mark is stripped first,
// for the same reason Render strips it: a real parser strips it too, and
// counting it as a rune on line 1 would shift every column after it by one.
func CheckNoTabs(filename string, src []byte) Diagnostics {
	src = stripLeadingBOM(src)
	s := string(src)

	type lineTabs struct {
		line, col, count int
	}
	var offending []lineTabs
	curIdx := -1 // index into offending of the line currently being tallied, or -1

	line, col := 1, 1
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch r {
		case '\n':
			line++
			col = 1
			curIdx = -1
		case '\r':
			line++
			col = 1
			curIdx = -1
			if i+1 < len(s) && s[i+1] == '\n' {
				i++ // "\r\n" is one line break, not two
			}
		case '\t':
			if curIdx == -1 || offending[curIdx].line != line {
				offending = append(offending, lineTabs{line: line, col: col})
				curIdx = len(offending) - 1
			}
			offending[curIdx].count++
			col++
		default:
			col++
		}
		i += size
	}

	var ds Diagnostics
	limit := len(offending)
	truncated := limit > maxTabDiagnostics
	if truncated {
		limit = maxTabDiagnostics
	}
	for _, lt := range offending[:limit] {
		ds.Add(tabDiagnostic(filename, lt.line, lt.col, lt.count))
	}
	if truncated {
		ds.Add(Diagnostic{
			Severity: Error,
			File:     filename,
			Message: fmt.Sprintf(
				"and %d more line(s) with tab characters (showing the first %d)",
				len(offending)-limit, maxTabDiagnostics,
			),
		})
	}
	return ds
}

// tabDiagnostic builds the Diagnostic for one offending line: count tabs
// found starting at column col on line, line and col both 1-based, col
// naming the first tab on the line.
func tabDiagnostic(filename string, line, col, count int) Diagnostic {
	message := "tab character is not allowed in schema files"
	if count > 1 {
		message = fmt.Sprintf("%d tab characters are not allowed in schema files", count)
	}
	return Diagnostic{
		Severity:  Error,
		File:      filename,
		Pos:       source.Pos{Line: line, Column: col},
		EndColumn: col + 1,
		Message:   message,
		Hint: "use spaces instead of tabs; YAML forbids tabs for indentation, and " +
			"lapigo forbids them everywhere else so reported column numbers stay correct",
	}
}
