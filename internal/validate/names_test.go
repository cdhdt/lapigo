package validate

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestValidateEntityStructNames_KeepsFirstPriorAcrossThreeWayCollision is the
// mutation-gap regression for validateEntityStructNames' `seen` map: on a
// three (or more) -way Go-identifier collision, every diagnostic after the
// first must blame the *first* declared member as "prior" -- not whichever
// member the previous diagnostic just compared against. seen[m.goName] is
// set exactly once, in the `else` branch that only runs the first time a
// goName is encountered, and must never be reassigned inside the collision
// branch; a mutant that reassigns it there (or unconditionally) would make
// the second diagnostic's "prior" silently become "aX" instead of staying
// "a_x".
//
// This cannot be exercised as a testdata/*.yaml golden fixture the way most
// of this package's rules are: any two plain fields whose GoName collides
// are already rejected by internal/parse's own checkFieldCollisions (see
// entityMembers' own doc comment) before an *ir.Schema exists at all, and
// any two `belongsTo` relations whose GoName collides also always collide on
// their own underlying FK field's GoName (goName(name)+"ID", set by
// internal/parse/relation.go), which checkFieldCollisions catches for the
// same reason. A field and a relation is the only pairing that reaches this
// function without also tripping a parse-time rejection first
// (relation_field_collision.yaml), and there is no third member available to
// extend that pairing into a three-way collision through parse.Parse.
//
// validateEntityStructNames is called directly here, on a hand-built
// *ir.Entity, the same way TestValidateSortKeyEligibility_UnknownFieldType
// (sort_test.go) reaches a FieldType no real parse.Parse output could ever
// produce.
func TestValidateEntityStructNames_KeepsFirstPriorAcrossThreeWayCollision(t *testing.T) {
	e := &ir.Entity{
		Name: "widget",
		Fields: []*ir.Field{
			{
				Name:   source.NewAt("a_x", source.Pos{Line: 2, Column: 7}, source.Pos{Line: 2, Column: 10}),
				GoName: "AX",
			},
			{
				Name:   source.NewAt("aX", source.Pos{Line: 3, Column: 7}, source.Pos{Line: 3, Column: 9}),
				GoName: "AX",
			},
			{
				Name:   source.NewAt("a__x", source.Pos{Line: 4, Column: 7}, source.Pos{Line: 4, Column: 11}),
				GoName: "AX",
			},
		},
	}

	var diags diag.Diagnostics
	validateEntityStructNames(e, "lapigo.yaml", &diags)

	want := diag.Diagnostics{
		{
			Severity:  diag.Error,
			File:      "lapigo.yaml",
			Pos:       source.Pos{Line: 3, Column: 7},
			EndColumn: 9,
			Message:   `field "a_x" and field "aX" of entity "widget" both produce Go identifier "AX"`,
			Hint:      "rename one of them so their generated Go identifiers don't collide (spec §5.6)",
		},
		{
			Severity:  diag.Error,
			File:      "lapigo.yaml",
			Pos:       source.Pos{Line: 4, Column: 7},
			EndColumn: 11,
			Message:   `field "a_x" and field "a__x" of entity "widget" both produce Go identifier "AX"`,
			Hint:      "rename one of them so their generated Go identifiers don't collide (spec §5.6)",
		},
	}

	if len(diags) != len(want) {
		t.Fatalf("len(diags) = %d, want %d: %+v", len(diags), len(want), diags)
	}
	for i := range want {
		if diags[i] != want[i] {
			t.Errorf("diags[%d] =\n%+v\nwant:\n%+v", i, diags[i], want[i])
		}
	}
}
