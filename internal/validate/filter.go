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
// ir.Filter carries no position of its own: spec §2.2 defines it as exactly
// `{Field *Field; Op FilterOp}`, matching ir.Filter's actual definition, and
// internal/parse discards the `filters:` list entry's own span once the
// Filter is resolved (internal/parse/schema.go's resolveSchema, second
// pass, builds `ir.Filter{Field: field, Op: ir.FilterOpEq}` from a
// source.At[string] it already has in hand and does not carry forward). The
// diagnostic below is positioned at the field's own declaration
// (Field.Name) instead: a real position, inside the correct entity, just not
// at the `filters:` entry itself. Fixing this precisely would mean adding a
// Pos to ir.Filter, which is out of this package's scope (see this
// package's own doc comment) and is reported in the final task summary
// instead of patched here.
func validateFilters(e *ir.Entity, file string, diags *diag.Diagnostics) {
	for _, filt := range e.Filters {
		f := filt.Field
		if f.Type != ir.FieldTypeJSON {
			continue
		}
		diags.Add(diag.Diagnostic{
			Severity:  diag.Error,
			File:      file,
			Pos:       f.Name.Pos,
			EndColumn: f.Name.End.Column,
			Message:   fmt.Sprintf("field %q has type \"json\" and may not be used as a filter", f.Name.Value),
			Hint:      fmt.Sprintf("json fields are neither sortable nor filterable (spec §3.4); remove `%s` from `filters`", f.Name.Value),
		})
	}
}
