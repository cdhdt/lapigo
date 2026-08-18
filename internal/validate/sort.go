package validate

import (
	"fmt"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
)

// validateSort applies spec §3.3 rules 2-5 to e's declared sort. Rule 1
// ("one direction for the whole spec") is internal/parse's job and already
// enforced before this package ever runs: ir.SortSpec has exactly one Desc
// field for the whole spec, so by the time an *ir.Schema exists there is no
// per-key direction left for a later validator to even compare (see
// internal/parse/sort.go's buildSortSpec doc comment, which spells out why
// that check cannot be deferred here).
func validateSort(e *ir.Entity, file string, diags *diag.Diagnostics) {
	keys := e.Sort.Keys
	for i, k := range keys {
		validateSortKeyEligibility(e, k, file, diags)
		if i == len(keys)-1 {
			validateSortKeyUniqueness(e, k, file, diags)
		}
		validateSortKeyMutability(e, k, file, diags)
	}
}

// validateSortKeyEligibility reports spec §3.3 rules 3 and 4 for one sort
// key: no sort key may be nullable, decimal or json. The eligibility
// decision itself is *ir.Field.IsSortEligible -- not reimplemented here.
// This function only turns a non-empty reason into a positioned diagnostic
// whose wording and hint are tailored to which rule fired, checked in the
// same order IsSortEligible itself checks them (nullability before type), so
// the branch taken here always matches the reason IsSortEligible actually
// returned.
func validateSortKeyEligibility(e *ir.Entity, k ir.SortKey, file string, diags *diag.Diagnostics) {
	f := k.Field
	if f.IsSortEligible() == "" {
		return
	}
	d := diag.Diagnostic{Severity: diag.Error, File: file, Pos: k.Span.Start, EndColumn: k.Span.End.Column}
	switch {
	case f.Nullable:
		d.Message = fmt.Sprintf("sort key %q must not be nullable", f.Name.Value)
		d.Hint = fmt.Sprintf("mark `%s` `required: true`, or remove it from `sort` (spec §3.3 rule 3)", f.Name.Value)
	case f.Type == ir.FieldTypeDecimal:
		d.Message = fmt.Sprintf("sort key %q has type %q, which may not be a sort key", f.Name.Value, f.Type.String())
		d.Hint = fmt.Sprintf("decimal values are not safely comparable through a JSON-encoded cursor; remove `%s` from `sort` (spec §3.3 rule 4)", f.Name.Value)
	case f.Type == ir.FieldTypeJSON:
		d.Message = fmt.Sprintf("sort key %q has type %q, which may not be a sort key", f.Name.Value, f.Type.String())
		d.Hint = fmt.Sprintf("json fields are neither sortable nor filterable (spec §3.4); remove `%s` from `sort`", f.Name.Value)
	default:
		// IsSortEligible found some other reason. Every FieldType this
		// package's callers can actually produce is covered by one of the
		// cases above (internal/parse never resolves an out-of-range
		// FieldType), so this branch cannot be reached from a real schema
		// -- it exists so a non-empty reason is never silently dropped on
		// the floor if that ever stops being true (see
		// ir.Field.IsSortEligible's own default case and its test,
		// TestField_IsSortEligible_UnknownTypeHasNoDefaultBranch, for the
		// defect this defends against on the ir side).
		d.Message = fmt.Sprintf("sort key %q may not be used: %s", f.Name.Value, f.IsSortEligible())
		d.Hint = fmt.Sprintf("remove `%s` from `sort`", f.Name.Value)
	}
	diags.Add(d)
}

// validateSortKeyUniqueness reports spec §3.3 rule 2: the last sort key must
// resolve from `pk: true` or `unique: true`. Checked only for the last
// element of e.Sort.Keys -- the constraint exists for the tiebreaker a
// keyset scan needs, not for every key in the spec.
//
// This is the rule spec §4.1's worked example documents byte for byte;
// TestValidate_Golden/sort_key_not_unique reproduces it exactly.
func validateSortKeyUniqueness(e *ir.Entity, k ir.SortKey, file string, diags *diag.Diagnostics) {
	f := k.Field
	if f.PK || f.Unique {
		return
	}
	pos, end := k.Span.Start, k.Span.End
	sign := ""
	if e.Sort.Desc {
		sign = "-"
	}
	pkName := ""
	if e.PK != nil {
		pkName = e.PK.Column
	}
	diags.Add(diag.Diagnostic{
		Severity:  diag.Error,
		File:      file,
		Pos:       pos,
		EndColumn: end.Column,
		Message:   fmt.Sprintf("sort key %q is not unique", f.Name.Value),
		Hint: fmt.Sprintf(
			"the last sort key must be unique; add a second key such as `%s%s`,\nor mark `%s` unique",
			sign, pkName, f.Name.Value),
	})
}

// validateSortKeyMutability reports spec §3.3 rule 5: a mutable sort key is
// a warning, not an error -- a row whose sort value changes can move
// relative to a live cursor, which is inherent to keyset pagination, not a
// defect the schema author needs to fix before generating (diag.Diagnostics
// treats a Warning-only accumulator as usable: see diag.Diagnostics.Err's
// doc comment).
//
// A field is treated as non-mutable -- and so exempt from this warning --
// when nothing the generated API can ever change about its value once the
// row exists: its own primary key (never part of CreateInput or
// UpdateInput, and never reassigned by a client), a `readonly:` field (never
// accepted from a request at all, spec §3.1), an `immutable:` field
// (accepted on create, rejected on update, spec §3.1), or a field carrying
// `default:` (excluded from CreateInput by spec §6.5, and therefore --
// because UpdateInput is built by removing fields from CreateInput's own set,
// never adding to it, per spec §6.5 -- also excluded from UpdateInput, so
// nothing in the generated API can ever set it after the database or the
// generator supplies it at insert).
//
// The task brief names only `immutable: true` and `default:` as making a
// field non-mutable; PK and ReadOnly are added here because both make a
// field client-immutable through the exact same §6.5 input-projection
// mechanism, and omitting them would warn on every plain `-id` tiebreaker --
// the single most common last sort key in the whole format.
func validateSortKeyMutability(e *ir.Entity, k ir.SortKey, file string, diags *diag.Diagnostics) {
	f := k.Field
	if f.PK || f.ReadOnly || f.Immutable || f.Default != nil {
		return
	}
	pos, end := k.Span.Start, k.Span.End
	diags.Add(diag.Diagnostic{
		Severity:  diag.Warning,
		File:      file,
		Pos:       pos,
		EndColumn: end.Column,
		Message:   fmt.Sprintf("sort key %q may change after the row is created", f.Name.Value),
		Hint: fmt.Sprintf(
			"a row whose sort value changes can move relative to a live cursor; this is inherent to keyset "+
				"pagination and does not block generation. Mark `%s` `immutable: true` if its value must never "+
				"change once set (spec §3.3 rule 5)",
			f.Name.Value),
	})
}
