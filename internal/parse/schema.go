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
// every entity first (buildEntity, via buildFieldsAndRelations), then, only
// after Schema.Entities has been sorted by name, every pointer that must
// reference one of them -- Entity.PK, SortKey.Field, Filter.Field and
// Relation.Target.
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
		builds = append(builds, entityBuild{entity: entity, nameAt: e.Key, relations: relations, sortKeys: sortKeys, filters: filters})
	}

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
	for _, b := range builds {
		for _, rel := range b.relations {
			r.resolvePendingRelation(schema, rel)
		}
		for _, sk := range b.sortKeys {
			field := b.entity.Lookup(sk.name.Value)
			if field == nil {
				r.addAt(sk.name, "", "sort key %q is not a field of entity %q", sk.name.Value, b.entity.Name)
				continue
			}
			b.entity.Sort.Keys = append(b.entity.Sort.Keys, ir.SortKey{Field: field, Pos: sk.name.Pos})
		}
		for _, filt := range b.filters {
			field := b.entity.Lookup(filt.name.Value)
			if field == nil {
				r.addAt(filt.name, "", "filter %q is not a field of entity %q", filt.name.Value, b.entity.Name)
				continue
			}
			b.entity.Filters = append(b.entity.Filters, ir.Filter{Field: field, Op: ir.FilterOpEq})
		}
	}

	return schema
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
