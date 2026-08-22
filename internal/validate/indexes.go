package validate

import (
	"fmt"
	"strings"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
)

// validateIndexes reports the semantic rules a declared composite index must
// satisfy (spec §3.5, §7.2). internal/parse has already resolved every column
// to a field of the same entity; what is left needs the whole entity to
// decide:
//
//  1. every column must be a declared filter of the entity, by pointer
//     identity -- an index exists to serve a filter combination, and a column
//     the API can never filter on serves no combination;
//  2. no column twice within one entry;
//  3. at least two columns -- the derived set of spec §7.2 already indexes
//     every single declared filter prefixed to the sort keys, so a
//     single-column entry would emit the same index twice;
//  4. no two entries with the same ordered column list -- same index, twice
//     the write cost.
//
// Every diagnostic blames the exact span of the offending `filters:` entry
// inside `indexes:`, never the field's own declaration (spec §2.2).
func validateIndexes(e *ir.Entity, file string, diags *diag.Diagnostics) {
	filterFields := make(map[*ir.Field]bool, len(e.Filters))
	for _, filt := range e.Filters {
		filterFields[filt.Field] = true
	}

	seenEntries := make(map[string]bool, len(e.Indexes))
	for _, idx := range e.Indexes {
		// internal/parse never appends an Index with zero columns (a partial
		// entry whose every column failed to resolve produces diagnostics and
		// a discarded schema), but Validate promises not to panic on any
		// schema it is handed, and a hand-built IR has no such guarantor.
		if len(idx.Columns) == 0 {
			continue
		}
		seenColumns := make(map[*ir.Field]bool, len(idx.Columns))
		var key strings.Builder
		for _, col := range idx.Columns {
			f := col.Field
			if !filterFields[f] {
				diags.Add(diag.Diagnostic{
					Severity:  diag.Error,
					File:      file,
					Pos:       col.Span.Start,
					EndColumn: col.Span.End.Column,
					Message:   fmt.Sprintf("declared index column %q is not a filter of entity %q", f.Name.Value, e.Name),
					Hint: fmt.Sprintf("add `%s` to `filters:` first: a declared index may only combine filters the entity declares (spec §3.5)",
						f.Name.Value),
				})
			}
			if seenColumns[f] {
				diags.Add(diag.Diagnostic{
					Severity:  diag.Error,
					File:      file,
					Pos:       col.Span.Start,
					EndColumn: col.Span.End.Column,
					Message:   fmt.Sprintf("declared index lists filter %q twice", f.Name.Value),
					Hint:      "remove the duplicate; an index names each filter column once (spec §3.5)",
				})
			}
			seenColumns[f] = true
			key.WriteString(f.Column)
			key.WriteByte(0)
		}

		// Rule 3 fires on the entry's own first column: there is no span for
		// "the entry" distinct from the columns it names, and the first
		// column is where the reader's eye lands on the offending line.
		first := idx.Columns[0]
		if len(idx.Columns) < 2 {
			diags.Add(diag.Diagnostic{
				Severity:  diag.Error,
				File:      file,
				Pos:       first.Span.Start,
				EndColumn: first.Span.End.Column,
				Message:   "declared index has a single filter column, which the derived index set already covers",
				Hint:      "add a second filter to index a combination, or delete the entry (spec §7.2)",
			})
		}
		if seenEntries[key.String()] {
			diags.Add(diag.Diagnostic{
				Severity:  diag.Error,
				File:      file,
				Pos:       first.Span.Start,
				EndColumn: first.Span.End.Column,
				Message:   "declared index duplicates an earlier declared index",
				Hint:      "delete this entry; two identical indexes double the write cost for no read benefit (spec §7.2)",
			})
		}
		seenEntries[key.String()] = true
	}
}
