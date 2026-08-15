package parse

import "testing"

// TestBuildEndpoints_DuplicateIsRejected is the regression test for defect
// 4's first case: `endpoints: [list, list]` produced two ir.EndpointList
// entries with zero diagnostics, which panics at generated-server startup
// (http.ServeMux.Handle: duplicate pattern registration).
func TestBuildEndpoints_DuplicateIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n    endpoints: [list, list]\n")
	want := "duplicate endpoint \"list\" in entity `article`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestBuildSortSpec_DuplicateKeyIsRejected is defect 4's second case:
// `sort: [-id, -id]` builds a keyset comparison `(id, id) < ($1, $2)`,
// meaningless SQL, with zero diagnostics.
func TestBuildSortSpec_DuplicateKeyIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n    sort: [-id, -id]\n")
	want := "duplicate sort key \"id\" in entity `article`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestBuildPendingFilters_DuplicateIsRejected is defect 4's third case:
// `filters: [id, id]` produces two identical ir.Filter entries.
func TestBuildPendingFilters_DuplicateIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n    filters: [id, id]\n")
	want := "duplicate filter \"id\" in entity `article`"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

// TestResolveSchema_DuplicateTableIsRejected is defect 4's fourth case: two
// entities sharing a `table:` (whether explicit or defaulted) both compile
// cleanly and panic at generated-server migration/startup with a duplicate
// table definition. This is schema-wide, not per-entity, so it cannot be
// caught while a single entity is being built.
func TestResolveSchema_DuplicateTableIsRejected(t *testing.T) {
	msg := firstMessage(t, "entities:\n"+
		"  article:\n    table: shared\n    fields:\n      id: { type: uuid, pk: true }\n"+
		"  post:\n    table: shared\n    fields:\n      id: { type: uuid, pk: true }\n")
	want := "duplicate table \"shared\" (already used by entity \"article\")"
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}
