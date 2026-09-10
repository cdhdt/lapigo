package validate

import (
	"fmt"
	"strings"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
)

// reservedQueryParamHint is the reserved-names clause shared by every
// diagnostic validateFilterReservedNames renders, built once from
// ir.ReservedQueryParams so wording stays in sync when that list grows
// (spec §6.9.4 promises it will, once step 8 lands the rest of the query
// contract) instead of being retyped here.
var reservedQueryParamHint = func() string {
	quoted := make([]string, len(ir.ReservedQueryParams))
	for i, name := range ir.ReservedQueryParams {
		quoted[i] = "`" + name + "`"
	}
	return strings.Join(quoted, " and ")
}()

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
//
// A field can fail both this check and validateFilterReservedNames' at once
// only if a json field were also named "limit"/"after", which would still
// report two diagnostics on the same `filters:` entry -- json's own case
// does not `continue` past the reserved-name check for that reason; each
// check is independent and reports whatever it finds.
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

// validateFilterReservedNames reports spec §6.9.4: a filter whose wire name
// -- Field.Column, per §6.9.2's "one vocabulary, used everywhere a field is
// named on the wire" -- is "limit" or "after" would be shadowed by
// pagination, which reserves both as query-parameter names. §5.6's posture
// is reject, never mangle, so this is a validator error, not a silent
// rename.
//
// The check runs against f.Column, never f.Name.Value: a scalar field's
// Column equals its declared name (internal/parse/field.go), so the two
// coincide there, but a `belongsTo` field's Column is its name plus "_id"
// (internal/parse/relation.go's `Column: name.Value + "_id"`) -- a relation
// named "limit" or "after" yields "limit_id"/"after_id" and is fine. Using
// f.Name.Value here would reject that legal schema.
//
// Like validateFilters above, the diagnostic blames Filter.Span -- the
// `filters:` list entry -- not the field's own `fields:` declaration.
func validateFilterReservedNames(e *ir.Entity, file string, diags *diag.Diagnostics) {
	for _, filt := range e.Filters {
		f := filt.Field
		if !ir.IsReservedQueryParam(f.Column) {
			continue
		}
		diags.Add(diag.Diagnostic{
			Severity:  diag.Error,
			File:      file,
			Pos:       filt.Span.Start,
			EndColumn: filt.Span.End.Column,
			Message:   fmt.Sprintf("filter %q has wire name %q, which is reserved for pagination", f.Name.Value, f.Column),
			Hint:      fmt.Sprintf("%s are reserved query-parameter names for pagination (spec §6.9.4); remove `%s` from `filters`", reservedQueryParamHint, f.Name.Value),
		})
	}
}
