package parse

import (
	"github.com/goccy/go-yaml/ast"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// relationKeys is the complete set of keys a `type: belongsTo` field's
// mapping may declare: the subset of the general field keys buildRelationField's
// own switch actually reads (`pk`, `required`, `unique`, `readonly`,
// `immutable` -- a belongsTo column can be marked `required:` to become
// NOT NULL, for instance), plus the two relation-only keys `target` and
// `on_delete`, plus `type` itself.
//
// Deliberately not fieldKeys plus the relation-only two, as an earlier
// version had it: fieldKeys also lists `max`, `values`, `default` and
// `version`, none of which buildRelationField's switch has a case for.
// Whitelisting them let checkUnknownKeys silently accept and then drop
// them -- `version: true` on a belongsTo in particular means the
// optimistic-concurrency column silently does not exist, with zero
// diagnostics telling the schema's author why. This list is exactly the
// keys the switch below handles, so checkUnknownKeys rejects everything
// else.
var relationKeys = []string{"type", "pk", "required", "unique", "readonly", "immutable", "target", "on_delete"}

// buildRelationField resolves one `type: belongsTo` field entry into its FK
// *ir.Field (the raw "author_id" column: Column, Name and the general
// options every field supports) and a *pendingRelation carrying the pieces
// that need the target entity to finish resolving (spec §3, §2.2's
// `Relation.Target *Entity // resolved`).
//
// Design decision, not dictated by the spec: `filters: [status, author]`
// in the canonical schema (spec §3) names "author" as a Filter, and
// Filter.Field is a *ir.Field -- there is no Filter-on-Relation shape in
// the IR. So a belongsTo entry produces *both* an ir.Field (the FK column,
// found by Entity.Lookup like any other field, usable as a sort key or
// filter) and an ir.Relation (the association metadata: target, GoType,
// on_delete). The field's Nullable is resolved here, in this same pass, from
// its own `required:`/`pk:` options -- exactly like any other field's. Its
// Type is the one piece only the second pass can finish: it is set below to
// FieldTypeUUID as a placeholder, correct for every belongsTo in the
// canonical schema (every PK in it is a uuid), and is overwritten once the
// target's real PK type is known (see resolvePendingRelations).
func (r *resolver) buildRelationField(e *ir.Entity, name source.At[string], body ast.Node) (*ir.Field, *pendingRelation) {
	r.requireExportableName(name, "field name")

	m, ok := r.requireMapping(body, fieldContext(name.Value))
	if !ok {
		return nil, nil
	}
	entries := r.entries(m)
	r.checkUnknownKeys(entries, fieldContext(name.Value), relationKeys)

	f := &ir.Field{
		Name:   name,
		GoName: goName(name.Value) + "ID",
		Column: name.Value + "_id",
		Type:   ir.FieldTypeUUID,
	}

	rel := &pendingRelation{
		entity:   e,
		field:    f,
		name:     name.Value,
		nameAt:   name,
		onDelete: "RESTRICT",
	}

	var required, pk bool
	var targetSeen bool
	for _, entry := range entries {
		switch entry.Key.Value {
		case "target":
			s, ok := r.requireString(entry.Value, fieldContext(name.Value)+" `target`")
			if !ok {
				// requireString already reported the wrong-type
				// diagnostic. targetSeen is still set to true here, for
				// the same reason buildField sets typeSeen
				// unconditionally: a bad value still means the key was
				// present, and "has no target" would be a misleading
				// second diagnostic alongside the wrong-type one.
				targetSeen = true
				continue
			}
			if s.Value == "" {
				// `target: ""` names no entity that could ever resolve --
				// there is no entity named the empty string, and no
				// "did you mean" suggestion is possible against an empty
				// query. This used to fall through to the general case
				// below with targetSeen forced true regardless of value,
				// which suppressed the "has no target" diagnostic entirely:
				// the FK field and column were still built, but zero
				// Relation was ever appended and zero diagnostic explained
				// why. Leaving targetSeen false here makes the check after
				// this loop fire exactly as it would for a field that
				// omitted `target:` altogether -- the fix is identical
				// either way ("add a `target:` key naming the entity this
				// belongsTo references"), so there is no need for a
				// second, separate message.
				continue
			}
			targetSeen = true
			rel.targetName = atOf(s.Value, s.GetToken())
		case "on_delete":
			s, ok := r.requireString(entry.Value, fieldContext(name.Value)+" `on_delete`")
			if !ok {
				continue
			}
			sql, ok := onDeleteKeywords[s.Value]
			if !ok {
				hint := "valid values are `restrict`, `cascade`, `set_null`"
				r.addAt(atOf(s.Value, s.GetToken()), hint, "unknown on_delete value %q", s.Value)
				continue
			}
			rel.onDelete = sql
		case "pk":
			b, ok := r.requireBool(entry.Value, fieldContext(name.Value)+" `pk`")
			if ok {
				pk = b.Value
				f.PK = b.Value
			}
		case "required":
			b, ok := r.requireBool(entry.Value, fieldContext(name.Value)+" `required`")
			if ok {
				required = b.Value
			}
		case "unique":
			b, ok := r.requireBool(entry.Value, fieldContext(name.Value)+" `unique`")
			if ok {
				f.Unique = b.Value
			}
		case "readonly":
			b, ok := r.requireBool(entry.Value, fieldContext(name.Value)+" `readonly`")
			if ok {
				f.ReadOnly = b.Value
			}
		case "immutable":
			b, ok := r.requireBool(entry.Value, fieldContext(name.Value)+" `immutable`")
			if ok {
				f.Immutable = b.Value
			}
		case "type":
			// Already established as "belongsTo" by isBelongsTo; nothing
			// further to resolve from it.
		}
	}

	if !targetSeen {
		r.addAt(name, "add a `target:` key naming the entity this belongsTo references",
			"belongsTo field %q has no `target`", name.Value)
	}

	f.Nullable = !required && !pk
	rel.nullable = f.Nullable

	return f, rel
}

// resolvePendingRelations resolves every pending belongsTo relation in the
// schema, iterating to a fixed point rather than a single declaration-order
// pass -- the regression this exists to prevent (defect 6): a plain,
// single-pass "for each relation, resolve" walk sets
// rel.field.Type = target.PK.Type using whatever target.PK.Type currently
// holds, which is only correct once target.PK has itself finished
// resolving. When target.PK is itself an unresolved belongsTo FK field
// (a PK that is a belongsTo to another PK that is a belongsTo, ...), a
// single pass in declaration order can visit the dependent relation before
// the one it depends on, and silently copies the FieldTypeUUID placeholder
// instead of the real, eventually-resolved type -- with zero diagnostics,
// since the resolved pointer identity is still correct and Freeze has
// nothing to compare it against.
//
// Each pass resolves every relation whose target's PK is not itself an
// unresolved FK field of another pending relation, then repeats over
// whatever is left. A pass that makes no progress means every remaining
// relation depends on another remaining relation's target PK -- a cycle of
// PK-belongsTo fields, which can never resolve a concrete type -- and is
// reported as its own diagnostic rather than looping forever.
func (r *resolver) resolvePendingRelations(schema *ir.Schema, all []*pendingRelation) {
	// fkOwner maps an FK *ir.Field back to the pendingRelation that will
	// finalize its Type, so resolving one relation can tell whether the
	// target's PK is a field this same fixed point still has to resolve.
	fkOwner := make(map[*ir.Field]*pendingRelation, len(all))
	for _, rel := range all {
		fkOwner[rel.field] = rel
	}

	resolved := make(map[*pendingRelation]bool, len(all))
	remaining := all
	for len(remaining) > 0 {
		var deferred []*pendingRelation
		for _, rel := range remaining {
			if rel.targetName.Value != "" {
				if target := schema.Lookup(rel.targetName.Value); target != nil && target.PK != nil {
					if depRel, isPendingFK := fkOwner[target.PK]; isPendingFK && !resolved[depRel] {
						// target's own PK has not finished resolving yet;
						// come back to this relation on a later pass.
						deferred = append(deferred, rel)
						continue
					}
				}
			}
			r.resolvePendingRelation(schema, rel)
			resolved[rel] = true
		}
		if len(deferred) == len(remaining) {
			// No relation in this pass could be resolved: every one of them
			// is waiting on another relation in the same set, which is
			// waiting in turn -- a cycle. Report each and stop; resolving
			// none of them further is safe, since a diagnostic already
			// means the schema will not be used (see Parse).
			for _, rel := range deferred {
				r.addAt(rel.nameAt,
					"break the cycle: give one entity in the chain a primary key that is not itself a belongsTo",
					"belongsTo field %q is part of a cycle of primary keys, whose type can never be resolved", rel.name)
			}
			return
		}
		remaining = deferred
	}
}

// resolvePendingRelation resolves rel.field's owning entity's Relation and
// finalizes rel.field's Type against target's own PK, once target has been
// found in the schema's sorted Entities. Called only after Schema.Entities
// is sorted (see resolveSchema) -- resolving a Relation.Target before that
// sort is exactly the silent-retargeting bug ir.Schema.Freeze exists to
// catch (spec §2.2).
func (r *resolver) resolvePendingRelation(schema *ir.Schema, rel *pendingRelation) {
	if rel.targetName.Value == "" {
		// rel.targetName only ever gets a non-empty Value when
		// buildRelationField found a well-typed, non-empty `target:`
		// string (see its own "target" case). Every other way to reach
		// this branch -- the key absent, `target: ""`, or `target:` holding
		// a non-string value -- is a case buildRelationField already
		// reported its own positioned diagnostic for (respectively "has no
		// target" for the first two, and requireString's own
		// wrong-type message for the third), so there is nothing left to
		// resolve or report here.
		return
	}
	target := schema.Lookup(rel.targetName.Value)
	if target == nil {
		names := make([]string, 0, len(schema.Entities))
		for _, e := range schema.Entities {
			names = append(names, e.Name)
		}
		hint := ""
		if s := suggest(rel.targetName.Value, names); s != "" {
			hint = "did you mean `" + s + "`?"
		}
		r.addAt(rel.targetName, hint, "belongsTo target %q does not exist", rel.targetName.Value)
		return
	}
	goType := "pgtype.UUID" // matches the placeholder Type buildRelationField gives the FK field
	if target.PK != nil {
		rel.field.Type = target.PK.Type
		// Relation.GoType is documented as "from the target's PK" (spec
		// §2.2) -- the target PK's own type, not the FK field's, which is
		// why this does not call rel.field.GoType(): the FK column is
		// usually nullable (no `required:` on a belongsTo is the common
		// case), and Field.GoType would then answer "*pgtype.UUID" for a Go
		// type meant to be assigned into the target's own (never nullable;
		// resolvePK forces it) PK field. target.PK.GoType() also handles an
		// enum PK correctly (its own Type.GoType cannot, by design -- see
		// FieldType.GoType's doc comment), which resolving through
		// target.PK.Type directly would not.
		goType = target.PK.GoType()
	}
	rel.entity.Relations = append(rel.entity.Relations, ir.Relation{
		Name:       rel.name,
		NameSpan:   rel.nameAt.Span(),
		GoName:     goName(rel.name),
		Target:     target,
		TargetSpan: rel.targetName.Span(),
		Column:     rel.field.Column,
		GoType:     goType,
		Nullable:   rel.nullable,
		OnDelete:   rel.onDelete,
	})
}
