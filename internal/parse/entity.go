package parse

import (
	"github.com/goccy/go-yaml/ast"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// entityKeys is the complete set of keys an entity's mapping may declare
// (spec §3).
var entityKeys = []string{"table", "fields", "sort", "filters", "endpoints"}

// pendingRelation is a belongsTo declaration whose target cannot be
// resolved until every entity in the schema has been built and
// Schema.Entities sorted (see resolveSchema's two-pass structure, and the
// parser brief's warning about resolving a target before that sort).
type pendingRelation struct {
	entity     *ir.Entity
	field      *ir.Field // the FK column Field, already in entity.Fields; Type is finalized once Target is known
	name       string
	nameAt     source.At[string]
	targetName source.At[string]
	onDelete   string
	nullable   bool
}

// pendingSortKey and pendingFilter are raw, same-entity references gathered
// while an entity's own fields: mapping is being walked, resolved to a
// *ir.Field pointer once that entity's Fields slice is complete. Same-entity
// lookups do not share the cross-entity "resolved before the sort"
// retargeting hazard Relation.Target does (Schema.Entities is what gets
// sorted, and neither of these points into it) -- and, in fact,
// Entity.Fields is already complete by the time either list is parsed
// here: buildEntity always finishes buildFieldsAndRelations before it even
// looks at the sort: or filters: node, regardless of which key came first
// in the written YAML. Resolving one of these immediately, rather than
// deferring it, would already be safe.
//
// They are carried into the second pass anyway, alongside relations, so
// that every "not found" diagnostic this package produces -- a bad sort
// key, a bad filter, a bad relation target -- flows through the same
// resolveSchema loop instead of splitting reporting between two different
// code paths.
// desc is deliberately not carried per-key: the spec's single sort
// direction (spec §3.3 rule 1) already lives on ir.SortSpec.Desc, returned
// separately by buildSortSpec, and nothing downstream ever needs to know
// which sign an individual key was written with once that single direction
// has been checked consistent -- an earlier version stored one here anyway
// and never read it back.
type pendingSortKey struct {
	name source.At[string]
}

type pendingFilter struct {
	name source.At[string]
}

// buildEntity resolves one `entities:` member's mapping into an *ir.Entity,
// gathering (but not resolving) its belongsTo relations, sort keys and
// filters for the second pass. name is the entity's own name, already
// reduced to a source.At[string] by the caller.
func (r *resolver) buildEntity(name source.At[string], body ast.Node) (*ir.Entity, []*pendingRelation, []pendingSortKey, []pendingFilter) {
	r.requireExportableName(name, "entity name")

	m, ok := r.requireMapping(body, entityContext(name.Value))
	if !ok {
		return nil, nil, nil, nil
	}
	entries := r.entries(m)
	r.checkUnknownKeys(entries, entityContext(name.Value), entityKeys)

	e := &ir.Entity{
		Name:   name.Value,
		GoName: goName(name.Value),
		Table:  defaultTableName(name.Value),
	}

	var fieldsNode ast.Node
	var sortNode, filtersNode, endpointsNode ast.Node
	for _, entry := range entries {
		switch entry.Key.Value {
		case "table":
			s, ok := r.requireString(entry.Value, entityContext(name.Value)+" `table`")
			if ok {
				at := atOf(s.Value, s.GetToken())
				r.requireIdentifier(at, "table name")
				e.Table = s.Value
			}
		case "fields":
			fieldsNode = entry.Value
		case "sort":
			sortNode = entry.Value
		case "filters":
			filtersNode = entry.Value
		case "endpoints":
			endpointsNode = entry.Value
		}
	}

	if fieldsNode == nil {
		r.addAt(name, "add a `fields:` mapping with at least a primary key",
			"entity %q has no `fields`", name.Value)
	} else {
		var relations []*pendingRelation
		e.Fields, relations = r.buildFieldsAndRelations(e, fieldsNode)
		r.checkFieldCollisions(e)
		var sortKeys []pendingSortKey
		if sortNode != nil {
			e.Sort.Desc, sortKeys = r.buildSortSpec(sortNode, name.Value)
		}
		var filters []pendingFilter
		if filtersNode != nil {
			filters = r.buildPendingFilters(filtersNode, name.Value)
		}
		e.Endpoints = r.buildEndpoints(endpointsNode, name.Value, e.Table)
		return e, relations, sortKeys, filters
	}

	e.Endpoints = r.buildEndpoints(endpointsNode, name.Value, e.Table)
	return e, nil, nil, nil
}

// buildFieldsAndRelations walks an entity's `fields:` mapping, building an
// *ir.Field for every plain field and a *pendingRelation (plus its own FK
// *ir.Field) for every `type: belongsTo` entry.
func (r *resolver) buildFieldsAndRelations(e *ir.Entity, node ast.Node) ([]*ir.Field, []*pendingRelation) {
	m, ok := r.requireMapping(node, entityContext(e.Name)+" `fields`")
	if !ok {
		return nil, nil
	}
	entries := r.entries(m)

	fields := make([]*ir.Field, 0, len(entries))
	var relations []*pendingRelation
	for _, entry := range entries {
		if isBelongsTo(entry.Value) {
			field, rel := r.buildRelationField(e, entry.Key, entry.Value)
			if field != nil {
				fields = append(fields, field)
			}
			if rel != nil {
				relations = append(relations, rel)
			}
			continue
		}
		f := r.buildField(entry.Key, e.GoName, entry.Value)
		if f != nil {
			fields = append(fields, f)
		}
	}
	return fields, relations
}

// checkFieldCollisions reports a diagnostic for every field of e beyond the
// first, in declaration order, whose Column or GoName collides with an
// earlier field's. This is the check defect 5 exists for: a `belongsTo`
// FK field's Column and GoName are derived from the relation name
// ("author" -> Column "author_id", GoName "AuthorID"), so a sibling scalar
// field explicitly named "author_id" produces the identical pair with no
// name collision at all ("author" != "author_id") -- Freeze cannot see it,
// since identity is correct and the field it finds by name is genuinely the
// only field with that Name. The DDL emitter would emit "author_id" twice
// and the generated struct would declare "AuthorID" twice; neither
// compiles.
func (r *resolver) checkFieldCollisions(e *ir.Entity) {
	byColumn := make(map[string]*ir.Field, len(e.Fields))
	byGoName := make(map[string]*ir.Field, len(e.Fields))
	for _, f := range e.Fields {
		if prior, ok := byColumn[f.Column]; ok {
			r.addAt(f.Name, "give each field a distinct column name",
				"field %q and field %q both use column %q", f.Name.Value, prior.Name.Value, f.Column)
		} else {
			byColumn[f.Column] = f
		}
		if prior, ok := byGoName[f.GoName]; ok {
			r.addAt(f.Name, "rename one field so their Go names don't collide (spec §5.6)",
				"field %q and field %q both produce Go field name %q", f.Name.Value, prior.Name.Value, f.GoName)
		} else {
			byGoName[f.GoName] = f
		}
	}
}

// isBelongsTo reports whether a field's option mapping declares
// `type: belongsTo`, without emitting any diagnostic of its own -- it is a
// dispatch check, run before either buildField or buildRelationField, both
// of which report their own diagnostics for anything actually wrong with
// the mapping.
func isBelongsTo(body ast.Node) bool {
	m, ok := body.(*ast.MappingNode)
	if !ok {
		return false
	}
	for _, mv := range m.Values {
		key, ok := mv.Key.(*ast.StringNode)
		if !ok || key.Value != "type" {
			continue
		}
		val, ok := mv.Value.(*ast.StringNode)
		return ok && val.Value == "belongsTo"
	}
	return false
}

func entityContext(name string) string {
	return "entity `" + name + "`"
}
