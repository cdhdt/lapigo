package ir

import "github.com/cdhdt/lapigo/internal/source"

// Relation is a `belongsTo` foreign key resolved to its target entity (spec
// §3). Target is a resolved *Entity pointer, not a name, for the same reason
// SortKey.Field and Filter.Field are resolved pointers: a template needs the
// target's shape (its PK type, in particular, since GoType is derived from
// it) without performing a lookup.
//
// GoName and GoType, phase 1 consumer status (issue #25, verified with
// `/usr/bin/grep -r` over non-test code):
//
//   - GoName DOES have a phase 1 consumer: internal/validate/names.go's
//     entityMembers compares it against every Field.GoName on the same
//     entity, to catch the Field/Relation collision internal/parse cannot
//     see (a plain field named "Author" and a `belongsTo` relation named
//     "author" both resolve to the Go name "Author"). It is not dead.
//   - GoType has NO phase 1 consumer anywhere. internal/ddl reads
//     rel.Target.PK.GoType() directly instead of rel.GoType (see
//     internal/parse/relation.go's own comment on why: the FK column's Go
//     type must come from the target's PK, not from a field's own,
//     possibly-nullable Type). Phase 1 exposes only the FK scalar, whose Go
//     name and type live on the Field, not here.
//
// Both fields are kept, not deleted: GoType is reserved for phase 1.5's
// relation expansion (issue #6), where an expanded relation becomes a nested
// object on the response model and needs the Go type name of that object —
// exactly what a bare foreign-key scalar does not need today. Removing it
// now would be removing IR surface a settled future phase already depends
// on, which is a decision for that issue, not a cleanup here.
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
