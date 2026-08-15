package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestResolveSchema_EntitiesOutOfAlphabeticalOrderAreSortedAndResolveCorrectly
// closes a mutation-testing gap named directly in the review: no fixture
// declared entities out of alphabetical order, so deleting the
// `sort.Slice` call on Schema.Entities in resolveSchema survived --
// nothing distinguished declaration order from sorted order. It is also
// the test spec §2.2 cites pointer slices for: "zebra" is declared first,
// naming "apple" (declared second, alphabetically first) as a belongsTo
// target. If relation resolution ran before the sort, or resolved against
// a snapshot taken before it, a naive []Entity (not []*Entity) would
// silently retarget the relation onto whatever entity the sort moved into
// that slot -- this test would catch it by checking pointer identity, not
// name.
func TestResolveSchema_EntitiesOutOfAlphabeticalOrderAreSortedAndResolveCorrectly(t *testing.T) {
	src := "entities:\n" +
		"  zebra:\n    fields:\n      id: { type: uuid, pk: true }\n      owner: { type: belongsTo, target: apple }\n" +
		"  apple:\n    fields:\n      id: { type: uuid, pk: true }\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	if len(diags) != 0 {
		t.Fatalf("diags = %+v, want none", diags)
	}
	if err := schema.Freeze(); err != nil {
		t.Fatalf("Freeze() = %v, want nil", err)
	}

	if len(schema.Entities) != 2 {
		t.Fatalf("len(Entities) = %d, want 2", len(schema.Entities))
	}
	if schema.Entities[0].Name != "apple" || schema.Entities[1].Name != "zebra" {
		t.Fatalf("Entities = [%s %s], want [apple zebra] (sorted, despite zebra being declared first)",
			schema.Entities[0].Name, schema.Entities[1].Name)
	}

	zebra := schema.Entities[1]
	if len(zebra.Relations) != 1 {
		t.Fatalf("zebra.Relations = %+v, want 1", zebra.Relations)
	}
	// Identity, not name: a target resolved before the sort (or against a
	// stale, unsorted snapshot) could still happen to carry the right Name
	// while pointing at the wrong *Entity if a decoy shared that name --
	// the same distinction TestParse_BelongsToFieldOptions makes.
	if zebra.Relations[0].Target != schema.Entities[0] {
		t.Errorf("zebra's relation Target is not the same *Entity as schema.Entities[0] (apple)")
	}
}

// TestBuildRelationField_OnDeleteDefaultsToRestrict closes the mutation gap
// "the on_delete default is never asserted. Flipping \"RESTRICT\" to
// \"CASCADE\" survives -- a silent data-loss change." Every existing
// belongsTo fixture that checks OnDelete writes `on_delete:` explicitly
// (canonical.yaml writes `on_delete: restrict`, which maps to "RESTRICT"
// via onDeleteKeywords regardless of what buildRelationField's own default
// literal is) -- none of them exercise the *default*, only the keyword
// mapping. This one omits on_delete entirely.
func TestBuildRelationField_OnDeleteDefaultsToRestrict(t *testing.T) {
	schema := parseOK(t, "entities:\n  article:\n    fields:\n"+
		"      id: { type: uuid, pk: true }\n"+
		"      author: { type: belongsTo, target: article }\n")

	if len(schema.Entities[0].Relations) != 1 {
		t.Fatalf("Relations = %+v, want 1", schema.Entities[0].Relations)
	}
	got := schema.Entities[0].Relations[0].OnDelete
	if got != "RESTRICT" {
		t.Fatalf("OnDelete = %q, want %q (the undeclared default)", got, "RESTRICT")
	}
}

// TestBuildSortSpec_SortKeyPosIsExact closes the mutation gap "SortKey.Pos
// is never asserted: testdata_test.go:31 zeroes it before every
// comparison. It is exactly what spec §4.1's headline diagnostic needs."
// stripPositions is deliberately not used here.
func TestBuildSortSpec_SortKeyPosIsExact(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n" +
		"      created_at: { type: timestamp, required: true }\n" +
		"    sort: [-created_at, -id]\n"

	schema := parseOK(t, src)

	keys := schema.Entities[0].Sort.Keys
	if len(keys) != 2 {
		t.Fatalf("len(Sort.Keys) = %d, want 2", len(keys))
	}
	// Line 6 is "    sort: [-created_at, -id]"; the key "-created_at"
	// starts right after "[", the "-" sign included (atOf spans the whole
	// written form, per TestAtOf_UsesExplicitValueButTokenSpan), and
	// "-id" starts after ", ".
	if keys[0].Pos != (source.Pos{Line: 6, Column: 12}) {
		t.Errorf("Sort.Keys[0].Pos = %+v, want {6 12}", keys[0].Pos)
	}
	if keys[1].Pos != (source.Pos{Line: 6, Column: 25}) {
		t.Errorf("Sort.Keys[1].Pos = %+v, want {6 25}", keys[1].Pos)
	}
	if keys[0].Field != schema.Entities[0].Lookup("created_at") {
		t.Errorf("Sort.Keys[0].Field is not the created_at field, by identity")
	}
}
