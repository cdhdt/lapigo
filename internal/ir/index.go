package ir

import "github.com/cdhdt/lapigo/internal/source"

// Index is one entity-level declared composite index (spec §3.5): the escape
// hatch by which a schema names a filter *combination* the derived per-filter
// index set of spec §7.2 does not cover. The declared filter columns, in the
// order written, become the leading columns of the emitted index; the DDL
// emitter appends the entity's sort keys after them, exactly as it does for
// every derived index.
//
// Index stores only the declared filter columns. The sort-key suffix is
// derived at emission time from Entity.Sort rather than stored here: storing
// a copy of the sort keys would let it drift from the spec the entity itself
// carries, and an index whose suffix disagreed with the entity's sort would
// silently stop serving the keyset scan it exists for.
type Index struct {
	Columns []IndexColumn // declared order; the order defines the index's column order
}

// IndexColumn is one filter column of a declared Index. Field is a resolved
// *Field pointer, not a name, for the same reason as SortKey.Field and
// Filter.Field: the DDL emitter needs the column without a lookup (spec §2.2).
//
// Span is the entry in the `filters:` list of the `indexes:` item, so a
// diagnostic about a declared index column blames the line that names it, not
// the field's own `fields:` declaration.
type IndexColumn struct {
	Field *Field
	Span  source.Span
}
