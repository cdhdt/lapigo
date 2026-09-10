package ir

import (
	"sort"

	"github.com/cdhdt/lapigo/internal/source"
)

// Schema is the fully resolved form of one lapigo.yaml file: every default
// expanded, every relation resolved to its target Entity, every sort key and
// filter resolved to its target Field. Templates consume a *Schema and
// nothing else — never YAML, never a parser AST (spec §2.2).
type Schema struct {
	// Entities is sorted by name once resolved. Freeze checks the ordering;
	// nothing before that point in the pipeline enforces it, so a Schema
	// under construction may be temporarily unsorted.
	//
	// The slice holds *Entity, not Entity, and this is not a style choice
	// (spec §2.2). A *Entity taken into a []Entity is invalidated by any
	// later append, and — far worse — sorting a []Entity silently retargets
	// it: a Relation.Target resolved before the sort would end up pointing
	// at whatever entity the sort moved into that slot, with no crash and no
	// nil. Pointer slices make identity survive both reallocation and
	// reordering; Freeze's append-and-sort test is what actually proves it.
	Entities []*Entity
}

// Lookup returns the entity named name, or nil if the schema has none, or if
// s itself is nil. Entities is small (one lapigo.yaml describes a handful of
// entities), so a linear scan over the sorted slice is simpler than
// maintaining a parallel map that could drift out of sync with it.
//
// Lookup returns the schema's own *Entity, the same pointer identity every
// resolved Relation.Target holds, never a copy.
func (s *Schema) Lookup(name string) *Entity {
	if s == nil {
		return nil
	}
	for _, e := range s.Entities {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// Entity is one resolved `entities:` member of a Schema (spec §2.2, §3).
//
// Entity has no Imports field. Imports are a property of an output file, not
// of an entity: a single entity's generation touches model, store, httpapi
// and hooks packages, each with a different import set, so one Imports field
// on Entity could not represent any of them correctly. The render step
// builds a per-file import set instead.
type Entity struct {
	Name      string      // as written in lapigo.yaml
	NameSpan  source.Span // where Name itself was written
	GoName    string      // validated Go identifier
	Table     string
	TableSpan source.Span // the `table:` value; zero when Table was defaulted, not written
	Fields    []*Field    // declaration order, not sorted — see Lookup. Pointer slice for the same reason as Schema.Entities.
	PK        *Field
	// Version is the promoted pointer to the field marked `version: true`
	// (spec §3.1, §3.6), mirroring PK so that spec §6.6's ETag and If-Match
	// machinery never has to rescan Fields to find the optimistic-
	// concurrency column. Unlike PK, nil is a legitimate value here: a
	// version column is optional (spec §3.1's "at most one"), not
	// required, so an entity declaring none simply has Version == nil.
	// Schema.Freeze checks that Version agrees with Fields, exactly as it
	// does for PK.
	Version   *Field
	Sort      SortSpec
	Filters   []Filter
	Indexes   []Index // declared composite indexes, declaration order (spec §3.5); the derived set of spec §7.2 is NOT stored — the DDL emitter computes it
	Relations []Relation
	Endpoints []Endpoint
}

// SortedFilters returns a copy of e.Filters sorted by field name, for spec
// §7.4's cursor fingerprint, which is computed over a canonical form:
// "filters sorted by name, values in declaration order."
//
// It does not sort e.Filters in place, and does not return e.Filters itself
// even when already sorted: internal/ddl's indexColumnLists
// (internal/ddl/ddl.go) walks e.Filters in declaration order to derive one
// index per declared filter (spec §7.2), so that field's own order must
// survive untouched. Field names are unique within an entity — Schema.Freeze
// rejects a duplicate before this method could ever be called on a frozen
// Schema — so the sort needs no tiebreaker to stay deterministic.
func (e *Entity) SortedFilters() []Filter {
	sorted := make([]Filter, len(e.Filters))
	copy(sorted, e.Filters)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Field.Name.Value < sorted[j].Field.Name.Value
	})
	return sorted
}

// Lookup returns the field named name, or nil if the entity has none. It
// searches Fields in declaration order and returns the same *Field pointer
// that SortKey.Field, Filter.Field and Entity.PK hold for the field once the
// IR is fully resolved.
func (e *Entity) Lookup(name string) *Field {
	for _, f := range e.Fields {
		if f.Name.Value == name {
			return f
		}
	}
	return nil
}
