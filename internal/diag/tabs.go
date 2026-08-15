package diag

import "github.com/cdhdt/lapigo/internal/ir"

// CheckNoTabs scans src for tab characters and returns one Diagnostic per
// occurrence, before any YAML parsing happens.
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
// Positions are counted in runes, matching ir.Pos: a tab following a
// multi-byte identifier is reported at its rune column, not its byte
// offset.
func CheckNoTabs(filename string, src []byte) Diagnostics {
	var ds Diagnostics

	line, col := 1, 1
	for _, r := range string(src) {
		switch r {
		case '\n':
			line++
			col = 1
		case '\t':
			ds.Add(Diagnostic{
				Severity:  Error,
				File:      filename,
				Pos:       ir.Pos{Line: line, Column: col},
				EndColumn: col + 1,
				Message:   "tab character is not allowed in schema files",
				Hint: "use spaces instead of tabs; YAML forbids tabs for indentation, and " +
					"lapigo forbids them everywhere else so reported column numbers stay correct",
			})
			col++
		default:
			col++
		}
	}

	return ds
}
