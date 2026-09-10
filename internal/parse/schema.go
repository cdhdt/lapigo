package parse

import (
	"fmt"
	"sort"

	"github.com/goccy/go-yaml/ast"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// rootKeys is the complete set of keys the schema file's top level may
// declare. Phase 1's format (spec §3) has exactly one: `entities:`.
var rootKeys = []string{"entities"}

// entityBuild is one entity's first-pass result: the *ir.Entity itself
// (Fields fully built) plus everything that still needs a second pass --
// belongsTo relations, sort keys and filters, all of which reference a
// *ir.Field or *ir.Entity that must already exist as a resolved pointer.
type entityBuild struct {
	entity    *ir.Entity
	nameAt    source.At[string]
	relations []*pendingRelation
	sortKeys  []pendingSortKey
	filters   []pendingFilter
	indexes   []pendingIndex
}

// resolveSchema walks the whole document body into a *ir.Schema, following
// the two-pass structure the parser brief mandates: every *ir.Field for
// every entity first (buildEntity, via buildFieldsAndRelations), then every
// pointer that must reference one of them.
//
// Entity.PK (resolvePK) and version-field validation (resolveVersion) are
// resolved in the first pass, immediately after each entity's own Fields
// are built -- they only ever look within that same entity's Fields, so
// they need nothing from any other entity and do not have to wait for
// Schema.Entities to exist at all, let alone be sorted. SortKey.Field,
// Filter.Field and Relation.Target are the ones that genuinely wait for the
// second pass: Relation.Target because it looks up a *different* entity by
// name, which is only safe once Schema.Entities has been sorted (see the
// second pass below); SortKey.Field and Filter.Field because the field they
// name might be a belongsTo column appended later in the same entity's
// `fields:` mapping than the `sort:`/`filters:` list that names it.
//
// A caller checks r.diags.HasErrors() after this returns: on any error the
// returned *ir.Schema may be incomplete (nil PK, unresolved relations) and
// must not be used.
func (r *resolver) resolveSchema(body ast.Node) *ir.Schema {
	root, ok := r.requireMapping(body, "the schema file")
	if !ok {
		return nil
	}
	entries := r.entries(root)
	r.checkUnknownKeys(entries, "the schema file", rootKeys)

	var entitiesNode ast.Node
	for _, e := range entries {
		if e.Key.Value == "entities" {
			entitiesNode = e.Value
		}
	}
	if entitiesNode == nil {
		r.addNode(root, "add a top-level `entities:` mapping", "schema file has no `entities`")
		return nil
	}

	entitiesMap, ok := r.requireMapping(entitiesNode, "`entities`")
	if !ok {
		return nil
	}
	entityEntries := r.entries(entitiesMap)
	if len(entityEntries) == 0 {
		r.addNode(entitiesNode, "declare at least one entity", "`entities` has no members")
		return nil
	}

	builds := make([]entityBuild, 0, len(entityEntries))
	for _, e := range entityEntries {
		entity, relations, sortKeys, sortWritten, filters, indexes := r.buildEntity(e.Key, e.Value)
		if entity == nil {
			continue
		}
		r.resolvePK(entity, e.Key)
		r.resolveVersion(entity)
		if !sortWritten && entity.PK != nil {
			// CLAUDE.md decision 4: "the validator rejects sorts that lack
			// [a unique tiebreaker] -- this must not be possible to
			// express." An entity that never writes `sort:` at all used to
			// resolve with e.Sort.Keys empty and zero diagnostics, which
			// was exactly that hole. The PK is unique by construction (spec
			// §3.1: exactly one per entity), so `[-<pk>]` is always a safe,
			// unambiguous default -- synthesized here as an ordinary
			// pendingSortKey, resolved by the same second-pass loop below
			// as any written key, so it needs no special-casing there.
			// source.Bare gives it the zero Span its own doc comment
			// promises for a value lapigo synthesized rather than a human
			// wrote (nothing was written here for a diagnostic to blame).
			//
			// Skipped when entity.PK is nil: resolvePK has already reported
			// its own diagnostic for that (no PK, or more than one), and
			// there is no field to default the tiebreaker to.
			entity.Sort.Desc = true
			sortKeys = append(sortKeys, pendingSortKey{name: source.Bare(entity.PK.Name.Value)})
		}
		builds = append(builds, entityBuild{entity: entity, nameAt: e.Key, relations: relations, sortKeys: sortKeys, filters: filters, indexes: indexes})
	}

	r.checkDuplicateTables(builds)

	schema := &ir.Schema{Entities: make([]*ir.Entity, len(builds))}
	for i, b := range builds {
		schema.Entities[i] = b.entity
	}
	sort.Slice(schema.Entities, func(i, j int) bool {
		return schema.Entities[i].Name < schema.Entities[j].Name
	})

	// Second pass. schema.Entities is sorted now: resolving Relation.Target
	// against it here, and not before, is what keeps every resolved
	// pointer aimed at the entity it actually names (spec §2.2).
	//
	// Relations are resolved together, across every entity, in one
	// fixed-point pass (resolvePendingRelations) rather than per-entity in
	// declaration order -- a transitive chain of PK-belongsTo fields needs
	// to see relations outside its own entity's list resolve first; see
	// resolvePendingRelations' doc comment (defect 6).
	var allRelations []*pendingRelation
	for _, b := range builds {
		allRelations = append(allRelations, b.relations...)
	}
	r.resolvePendingRelations(schema, allRelations)

	for _, b := range builds {
		for _, sk := range b.sortKeys {
			field := b.entity.Lookup(sk.name.Value)
			if field == nil {
				r.addAt(sk.name, "", "sort key %q is not a field of entity %q", sk.name.Value, b.entity.Name)
				continue
			}
			b.entity.Sort.Keys = append(b.entity.Sort.Keys, ir.SortKey{Field: field, Span: sk.name.Span()})
		}
		for _, filt := range b.filters {
			field := b.entity.Lookup(filt.name.Value)
			if field == nil {
				r.addAt(filt.name, "", "filter %q is not a field of entity %q", filt.name.Value, b.entity.Name)
				continue
			}
			b.entity.Filters = append(b.entity.Filters, ir.Filter{Field: field, Op: ir.FilterOpEq, Span: filt.name.Span()})
		}
		for _, idx := range b.indexes {
			columns := make([]ir.IndexColumn, 0, len(idx.columns))
			for _, col := range idx.columns {
				field := b.entity.Lookup(col.name.Value)
				if field == nil {
					r.addAt(col.name, "", "index column %q is not a field of entity %q", col.name.Value, b.entity.Name)
					continue
				}
				columns = append(columns, ir.IndexColumn{Field: field, Span: col.name.Span()})
			}
			if len(columns) > 0 {
				b.entity.Indexes = append(b.entity.Indexes, ir.Index{Columns: columns})
			}
		}
	}

	return schema
}

// checkDuplicateTables reports a diagnostic for every entity beyond the
// first, in declaration order, whose Table (explicit or defaulted) collides
// with an earlier entity's -- two entities mapped to the same table compile
// cleanly today and panic identically to a duplicate endpoint, only at
// migration/startup instead of routing: DDL cannot create the same table
// twice, and neither can the generated store disambiguate which entity a
// row belongs to.
func (r *resolver) checkDuplicateTables(builds []entityBuild) {
	firstByTable := make(map[string]entityBuild, len(builds))
	for _, b := range builds {
		if prior, ok := firstByTable[b.entity.Table]; ok {
			r.addAt(b.nameAt,
				"give each entity a distinct `table:`, or accept the default (entity name + \"s\")",
				"duplicate table %q (already used by entity %q)", b.entity.Table, prior.entity.Name)
			continue
		}
		firstByTable[b.entity.Table] = b
	}
}

// resolvePK finds the field marked `pk: true` among e's own fields and sets
// e.PK to it, reporting a diagnostic -- with a position, before
// ir.Schema.Freeze ever runs -- for the two conditions Freeze would
// otherwise catch as a bare, positionless error: no field marked pk, or
// more than one.
func (r *resolver) resolvePK(e *ir.Entity, nameAt source.At[string]) {
	var pks []*ir.Field
	for _, f := range e.Fields {
		if f.PK {
			pks = append(pks, f)
		}
	}
	switch len(pks) {
	case 0:
		r.addAt(nameAt, "mark exactly one field `pk: true`",
			"entity %q has no field marked `pk: true`", e.Name)
	case 1:
		e.PK = pks[0]
	default:
		for _, f := range pks {
			r.addAt(f.Name, "exactly one field may be marked `pk: true`",
				"entity %q has %d fields marked `pk: true`, want exactly 1", e.Name, len(pks))
		}
	}
}

// resolveVersion reports a diagnostic -- with a position, before
// ir.Schema.Freeze ever runs -- for the one condition spec §3.1 states and
// Freeze would otherwise catch as a bare, positionless "this is a bug in
// lapigo" error: more than one field marked `version: true` in the same
// entity. Unlike resolvePK, there is no "zero" case to reject: a version
// column is optional (spec §3.1's "at most one"), not required.
//
// Beside resolvePK deliberately, not folded into it: the two checks share
// nothing but the "find every field with a bool flag set, and complain
// about more than one" shape, and PK has a zero-case resolvePK also has to
// handle that Version does not.
//
// When exactly one field carries `version: true`, resolveVersion hands it to
// validateVersionField for the three constraints issue #46 settles: type,
// nullability, and PK exclusivity. Those checks only make sense once there
// is a single, unambiguous field to blame -- the duplicate case above is
// already an error in its own right, and reporting "wrong type"/"nullable"
// against two candidates a user must first disambiguate would pile a second
// class of diagnostic onto a schema that has not yet said which field it
// means.
func (r *resolver) resolveVersion(e *ir.Entity) {
	var versions []*ir.Field
	for _, f := range e.Fields {
		if f.Version {
			versions = append(versions, f)
		}
	}
	switch len(versions) {
	case 0:
		return
	case 1:
		r.validateVersionField(e, versions[0])
	default:
		for _, f := range versions {
			r.addAt(f.Name, "at most one field may be marked `version: true` (spec §3.1)",
				"entity %q has %d fields marked `version: true`, want at most 1", e.Name, len(versions))
		}
	}
}

// validateVersionField reports a diagnostic -- with a position, before
// ir.Schema.Freeze ever runs -- for each of the three constraints issue #46
// settles on e's single `version: true` field, f:
//
//  1. its type must be `int` or `bigint`: step 7's optimistic-concurrency
//     update emits `SET version = version + 1`, which only an integer column
//     can execute;
//  2. it must be `required: true`: a NULL version makes every
//     `version = $n` comparison yield unknown, so an `If-Match` update
//     matches zero rows and returns 409 forever -- spec §3.3 rule 3's
//     reasoning ("SQL comparison against NULL yields unknown") applied to
//     this column instead of a sort key;
//  3. it may not also carry `pk: true` -- a row's identity and its
//     optimistic-concurrency counter are different columns by construction
//     (spec §3.1's own worked example never combines them).
//
// The three are independent, checked in this order, and none returns early
// on another firing: the issue's own trap example, `id: { type: uuid, pk:
// true, version: true }`, fails both the type check (uuid) and the PK check
// at once, and a user fixing only one should still see the other. This
// mirrors every other check in this package (spec §4.4: never stop at the
// first diagnostic).
//
// Deliberately NOT done here, and not anywhere else in this package: marking
// the field ReadOnly, or otherwise excluding it from input projection. Doing
// so would make internal/validate's validateSortKeyMutability treat it as
// immutable and silently drop the mutable-sort-key warning for a column the
// server bumps on every write -- the single worst possible sort key. The
// input-projection exclusion is a spec concern (issue #18), not something
// this change implements.
func (r *resolver) validateVersionField(e *ir.Entity, f *ir.Field) {
	if f.Type != ir.FieldTypeInt && f.Type != ir.FieldTypeBigint {
		r.addAt(f.Name,
			fmt.Sprintf("mark `%s` `type: int` or `type: bigint`; an optimistic-concurrency counter must be "+
				"an integer the store can increment", f.Name.Value),
			"entity %q's version field %q has type %q, want `int` or `bigint`", e.Name, f.Name.Value, f.Type.String())
	}
	if f.Nullable {
		r.addAt(f.Name,
			fmt.Sprintf("mark `%s` `required: true`; a NULL version makes every `version = $n` comparison "+
				"yield unknown, so an `If-Match` update matches zero rows and returns 409 forever "+
				"(spec §3.3 rule 3's reasoning, applied to this column)", f.Name.Value),
			"entity %q's version field %q is nullable", e.Name, f.Name.Value)
	}
	if f.PK {
		r.addAt(f.Name,
			fmt.Sprintf("mark `pk: true` on a different field, or remove `version: true` from `%s`", f.Name.Value),
			"entity %q's version field %q may not also be the primary key", e.Name, f.Name.Value)
	}
}
