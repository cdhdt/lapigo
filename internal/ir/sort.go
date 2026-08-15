package ir

import "github.com/cdhdt/lapigo/internal/source"

// SortSpec is an entity's single declared sort (spec §3, §7.1). Phase 1
// allows exactly one sort per entity and one direction for the whole spec:
// a row-value comparison — the only form verified to hold an index seek — is
// lexicographic only under a single operator, so mixed-direction keys
// (`-created_at, +id`) cannot use it and are rejected by the validator, not
// by this type.
type SortSpec struct {
	Keys []SortKey // last key resolves to a unique field
	Desc bool      // true for descending, false for ascending; applies to every key
}

// Direction returns the SQL keyword for s's single direction: "ASC" or
// "DESC".
func (s SortSpec) Direction() string {
	if s.Desc {
		return "DESC"
	}
	return "ASC"
}

// SortKey is one key of a SortSpec. Field is a resolved *Field pointer, not
// a name — see spec §2.2: a template rendering a keyset comparison needs the
// field's Go type, column and nullability directly, and a name would force a
// lookup inside the template, which spec §5.1 forbids.
type SortKey struct {
	Field *Field
	Pos   source.Pos
}
