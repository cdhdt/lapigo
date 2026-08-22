package validate

import (
	"fmt"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
)

// validateRelations reports the one semantic rule that needs the whole
// entity's resolved relations and the DDL the emitter will produce from them:
// `on_delete: set_null` on a non-nullable relation.
//
// The DDL emits the FK clause the schema asked for -- a NOT NULL column with
// ON DELETE SET NULL is accepted by Postgres at migration time and then fails
// on every delete of a referenced row ("null value in column ... violates
// not-null constraint"), surfacing as a 500 from generated code the schema
// itself made unsatisfiable. That is exactly the class of latent runtime bug
// this package exists to make unexpressible, so the combination is rejected
// at generation time, at the relation's own declaration.
//
// The diagnostic blames Relation.NameSpan -- the `fields:` entry that declares
// the relation -- because ir.Relation carries no span for the `on_delete:`
// value itself (spec §2.2's IR has NameSpan and TargetSpan only), and the
// declaration is where a reader fixes either side of the contradiction.
func validateRelations(e *ir.Entity, file string, diags *diag.Diagnostics) {
	for _, rel := range e.Relations {
		if rel.OnDelete != "SET NULL" || rel.Nullable {
			continue
		}
		diags.Add(diag.Diagnostic{
			Severity:  diag.Error,
			File:      file,
			Pos:       rel.NameSpan.Start,
			EndColumn: rel.NameSpan.End.Column,
			Message:   fmt.Sprintf("relation %q of entity %q is not nullable but declares on_delete: set_null", rel.Name, e.Name),
			Hint: "ON DELETE SET NULL cannot null a NOT NULL column: the migration applies, then every delete " +
				"of a referenced row fails. Make the relation optional, or use on_delete: restrict or cascade",
		})
	}
}
