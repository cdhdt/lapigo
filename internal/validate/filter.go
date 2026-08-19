package validate

import (
	"fmt"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
)

// validateFilters reports spec §3.4: json fields are neither sortable nor
// filterable. Sort-key eligibility for json is already covered by
// validateSortKeyEligibility, through Field.IsSortEligible; this is the
// filter half of the same rule -- IsSortEligible does not apply to Filters
// at all (it is a sort-key question, per its own doc comment: "It does not
// check uniqueness: that constraint applies to the last key of a SortSpec,
// not to every field the spec touches"), so this check is written directly
// against Field.Type rather than through it.
//
// The diagnostic is positioned at Filter.Span -- the `filters:` list entry
// itself, not the filtered field's `fields:` declaration. An earlier
// revision had no span on ir.Filter at all and pointed here at the field's
// own Name instead, which is a real position but the wrong line whenever
// `filters:` sits apart from `fields:` in the file (spec §2.2's own account
// of why this was worth fixing).
func validateFilters(e *ir.Entity, file string, diags *diag.Diagnostics) {
	for _, filt := range e.Filters {
		f := filt.Field
		if f.Type != ir.FieldTypeJSON {
			continue
		}
		diags.Add(diag.Diagnostic{
			Severity:  diag.Error,
			File:      file,
			Pos:       filt.Span.Start,
			EndColumn: filt.Span.End.Column,
			Message:   fmt.Sprintf("field %q has type \"json\" and may not be used as a filter", f.Name.Value),
			Hint:      fmt.Sprintf("json fields are neither sortable nor filterable (spec §3.4); remove `%s` from `filters`", f.Name.Value),
		})
	}
}
