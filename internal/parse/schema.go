package parse

import (
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
		entity, relations, sortKeys, filters := r.buildEntity(e.Key, e.Value)
		if entity == nil {
			continue
		}
		r.resolvePK(entity, e.Key)
		r.resolveVersion(entity)
		builds = append(builds, entityBuild{entity: entity, nameAt: e.Key, relations: relations, sortKeys: sortKeys, filters: filters})
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
func (r *resolver) resolveVersion(e *ir.Entity) {
	var versions []*ir.Field
	for _, f := range e.Fields {
		if f.Version {
			versions = append(versions, f)
		}
	}
	if len(versions) <= 1 {
		return
	}
	for _, f := range versions {
		r.addAt(f.Name, "at most one field may be marked `version: true` (spec §3.1)",
			"entity %q has %d fields marked `version: true`, want at most 1", e.Name, len(versions))
	}
}
