package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestResolvePendingRelations_TransitiveBelongsToTakesTargetsRealType is the
// regression test for defect 6: resolvePendingRelation sets
// rel.field.Type = target.PK.Type, but the second pass processed pending
// relations in declaration order -- if the target's own PK is itself an
// unresolved belongsTo FK, its Type was still the FieldTypeUUID placeholder
// buildRelationField gives every FK field, not its real, eventually-resolved
// type.
//
// c's PK is a belongsTo to b; b's PK is a belongsTo to a; a's PK is a plain
// bigint (deliberately not uuid, so the placeholder and the correct answer
// differ and a coincidental match cannot hide the bug). Entities are
// declared in dependency-reversed order (c, then b, then a) -- exactly the
// ordering that made the second pass visit c's relation before b's had
// resolved b's own PK type in the pre-fix code.
func TestResolvePendingRelations_TransitiveBelongsToTakesTargetsRealType(t *testing.T) {
	src := "entities:\n" +
		"  c:\n    fields:\n      id: { type: belongsTo, target: b, pk: true }\n" +
		"  b:\n    fields:\n      id: { type: belongsTo, target: a, pk: true }\n" +
		"  a:\n    fields:\n      id: { type: bigint, pk: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if err := schema.Freeze(); err != nil {
		t.Fatalf("Freeze() = %v, want nil", err)
	}

	b := schema.Lookup("b")
	c := schema.Lookup("c")
	if b == nil || c == nil {
		t.Fatal("schema.Lookup(b) or Lookup(c) = nil")
	}
	if b.PK.Type != ir.FieldTypeBigint {
		t.Errorf("b.PK.Type = %v, want FieldTypeBigint (from a's real PK type, not the belongsTo placeholder)", b.PK.Type)
	}
	if c.PK.Type != ir.FieldTypeBigint {
		t.Errorf("c.PK.Type = %v, want FieldTypeBigint (transitively, from a via b)", c.PK.Type)
	}
}

// TestResolvePendingRelations_CycleOfPKBelongsToIsRejected asserts the
// fixed-point resolver's other half: two entities whose PKs are each a
// belongsTo pointing at the other can never resolve a concrete type, and
// must be reported rather than looping forever or silently keeping the
// FieldTypeUUID placeholder with zero diagnostics.
func TestResolvePendingRelations_CycleOfPKBelongsToIsRejected(t *testing.T) {
	src := "entities:\n" +
		"  x:\n    fields:\n      id: { type: belongsTo, target: y, pk: true }\n" +
		"  y:\n    fields:\n      id: { type: belongsTo, target: x, pk: true }\n"

	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 2 {
		t.Fatalf("diags = %+v, want 2 (one per entity in the cycle)", diags)
	}
	for _, d := range diags {
		want := `belongsTo field "id" is part of a cycle of primary keys, whose type can never be resolved`
		if d.Message != want {
			t.Errorf("Message = %q, want %q", d.Message, want)
		}
	}
}
