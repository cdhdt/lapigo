// Package validate implements the semantic checks that need the whole
// resolved schema to decide (spec §3.3, §3.4, §5.6): the ones internal/parse
// cannot make while it is still building one entity's *ir.Field at a time.
//
// Validate runs on an *ir.Schema internal/parse already produced and
// ir.Schema.Freeze already accepted -- structure is sound (every resolved
// pointer belongs to the collection it claims to, exactly one PK per entity,
// no duplicate field names within an entity) by the time this package ever
// sees it. What is left is meaning: is the sort spec safe for keyset
// pagination, are filters and sort keys legal for their field's type, and do
// the Go identifiers the generator is about to emit collide with each other.
//
// This package never modifies internal/{source,ir,diag,parse}; where one of
// them cannot express what a diagnostic here would ideally point at (see
// sortKeySpan and validateFilters' doc comments for two such gaps), the
// limitation is documented and worked around, not patched upstream.
package validate

import (
	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
)

// Validate accumulates every diagnostic schema's sort specs, filters and
// generated-identifier collisions produce, across every entity, and returns
// them together -- never stopping at the first one (spec §4.4). file is the
// name stamped on every diagnostic (see diag.Diagnostic.File), matching the
// name a caller would also pass to internal/parse.Parse for the same
// source.File, so a diagnostic from this package sorts and renders
// identically alongside one from internal/parse (spec §4.4's "one
// diagnostic type... sorted together, printed once").
//
// schema is assumed frozen (ir.Schema.Freeze returned nil for it) and file
// non-empty; Validate does not call Freeze itself; a caller that skips it is
// asking this package to trust pointers it has no way to verify.
func Validate(schema *ir.Schema, file string) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, e := range schema.Entities {
		validateSort(e, file, &diags)
		validateFilters(e, file, &diags)
		validateEntityStructNames(e, file, &diags)
	}
	validatePackageNames(schema, file, &diags)
	return diags
}
