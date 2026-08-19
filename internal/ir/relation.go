package ir

import "github.com/cdhdt/lapigo/internal/source"

// Relation is a `belongsTo` foreign key resolved to its target entity (spec
// §3). Target is a resolved *Entity pointer, not a name, for the same reason
// SortKey.Field and Filter.Field are resolved pointers: a template needs the
// target's shape (its PK type, in particular, since GoType is derived from
// it) without performing a lookup.
type Relation struct {
	Name       string      // "author"
	NameSpan   source.Span // where "author" itself was written
	GoName     string      // "Author"
	Target     *Entity     // resolved
	TargetSpan source.Span // the `target:` value, for a diagnostic about a bad reference
	Column     string      // "author_id"
	GoType     string      // from the target's PK
	Nullable   bool
	OnDelete   string // "RESTRICT" by default (spec §3.1)
}
