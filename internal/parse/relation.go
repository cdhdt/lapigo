package parse

import (
	"github.com/goccy/go-yaml/ast"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// relationKeys is the complete set of keys a `type: belongsTo` field's
// mapping may declare -- the general field keys (`required`, `unique`,
// `readonly`, ...) still apply (a belongsTo column can be marked
// `required:` to become NOT NULL, for instance), plus the two relation-only
// keys `target` and `on_delete`.
var relationKeys = append(append([]string{}, fieldKeys...), "target", "on_delete")

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
// on_delete). The field's Type and Nullable are only finalized in the
// second pass, once the target's PK field is known (see resolvePendingRelations);
// until then it carries FieldTypeUUID as a placeholder -- correct for every
// belongsTo in the canonical schema, since every PK in it is a uuid, and
// overwritten regardless once the real target PK type is resolved.
func (r *resolver) buildRelationField(e *ir.Entity, name source.At[string], body ast.Node) (*ir.Field, *pendingRelation) {
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
			// targetSeen is set regardless of whether the value
			// type-checks, for the same reason buildField sets typeSeen
			// unconditionally: a bad value still means the key was
			// present, and "has no target" would be a misleading second
			// diagnostic alongside the wrong-type one.
			targetSeen = true
			s, ok := r.requireString(entry.Value, fieldContext(name.Value)+" `target`")
			if ok {
				rel.targetName = atOf(s.Value, s.GetToken())
			}
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

// resolvePendingRelation resolves rel.field's owning entity's Relation and
// finalizes rel.field's Type against target's own PK, once target has been
// found in the schema's sorted Entities. Called only after Schema.Entities
// is sorted (see resolveSchema) -- resolving a Relation.Target before that
// sort is exactly the silent-retargeting bug ir.Schema.Freeze exists to
// catch (spec §2.2).
func (r *resolver) resolvePendingRelation(schema *ir.Schema, rel *pendingRelation) {
	if rel.targetName.Value == "" {
		// buildRelationField already reported the missing `target:`.
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
		Name:     rel.name,
		GoName:   goName(rel.name),
		Target:   target,
		Column:   rel.field.Column,
		GoType:   goType,
		Nullable: rel.nullable,
		OnDelete: rel.onDelete,
	})
}
