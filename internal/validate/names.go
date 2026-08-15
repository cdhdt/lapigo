package validate

import (
	"fmt"
	"strings"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// reservedMethodNames are the methods the generator is expected to emit, per
// entity, on the struct(s) that carry that entity's fields as Go struct
// fields -- the model type, and the CreateInput/UpdateInput types spec §6.5
// derives from the same field set. "Validate" is the task brief's own
// example of a name that must be reserved: input validation against
// `required`, `max`, and enum membership (spec §6.5) has to live in a
// method with some fixed name, and a field whose exported Go name is
// "Validate" would redeclare it.
//
// internal/gen does not exist yet -- this package is built ahead of it, per
// the phase 1 build order (spec §10: validator is step 3, templates are
// steps 5-7) -- so this list cannot be derived from the templates it names;
// it is asserted here from the spec text alone.
//
// This list MUST be kept in sync with internal/gen's templates once they
// exist. A method added there without a matching entry added here is a
// schema that validates clean today and fails to compile only once
// regenerated against the real templates -- silently, since nothing but
// this comment links the two.
var reservedMethodNames = []string{
	"Validate",
}

// reservedMethodNameSet is reservedMethodNames as a set, built once from the
// literal slice above. It is only ever tested for membership in this
// package, never ranged for its own iteration order, so the fact that a Go
// map's iteration order is process-randomised never reaches a diagnostic's
// content or position -- unlike the defect internal/parse's own
// determinism_test.go documents (a vocabulary built once, at init, by
// ranging a map).
var reservedMethodNameSet = func() map[string]bool {
	m := make(map[string]bool, len(reservedMethodNames))
	for _, n := range reservedMethodNames {
		m[n] = true
	}
	return m
}()

// entityMember is one contributor to an entity's Go struct namespace: either
// a plain *ir.Field or a belongsTo relation, both of which become a field on
// the entity's generated model struct.
//
// Collecting both into one slice is what lets validateEntityStructNames
// catch a Field/Relation collision internal/parse cannot: parse's own
// checkFieldCollisions (internal/parse/entity.go) walks only e.Fields,
// because e.Relations does not exist yet at the point it runs -- relations
// resolve in internal/parse/schema.go's resolveSchema, in its second pass,
// strictly after checkFieldCollisions has already returned for every entity.
// A plain field named "Author" and a `type: belongsTo` field named "author"
// (distinct YAML keys -- "Author" != "author" -- so goccy's own duplicate-key
// rejection never sees them as the same key) both resolve to the Go name
// "Author": the field directly, the relation through
// internal/parse/relation.go's `GoName: goName(rel.name)`. Nothing in
// internal/parse ever compares those two names against each other.
type entityMember struct {
	goName   string
	label    string // e.g. `field "author_id"` or `relation "author"`
	pos, end source.Pos
}

// entityMembers returns e's Fields and Relations as one ordered slice of
// entityMember, Fields first (declaration order, per ir.Entity.Fields' own
// doc comment) then Relations (in whatever order internal/parse's
// resolvePendingRelations finished resolving them -- also a plain slice,
// never a map, so the order is deterministic for a given input even where it
// is not strictly declaration order). Neither loop ranges a map, so this
// function's output order depends only on the input schema, never on
// process-randomised map iteration.
func entityMembers(e *ir.Entity) []entityMember {
	members := make([]entityMember, 0, len(e.Fields)+len(e.Relations))
	for _, f := range e.Fields {
		members = append(members, entityMember{
			goName: f.GoName,
			label:  fmt.Sprintf("field %q", f.Name.Value),
			pos:    f.Name.Pos,
			end:    f.Name.End,
		})
	}
	for _, rel := range e.Relations {
		var pos, end source.Pos
		// The relation's own FK column field shares its Name.Value with the
		// relation -- both are resolved from the same `type: belongsTo`
		// YAML entry (internal/parse/relation.go's buildRelationField sets
		// the FK field's Name to the belongsTo key itself, and
		// resolvePendingRelation sets Relation.Name from the same
		// pendingRelation.name) -- so looking it up by that shared name is
		// a genuine, exact anchor for a diagnostic about the relation, not
		// a guess. ir.Relation itself carries no position: spec §2.2
		// defines it without one.
		if fk := e.Lookup(rel.Name); fk != nil {
			pos, end = fk.Name.Pos, fk.Name.End
		}
		members = append(members, entityMember{
			goName: rel.GoName,
			label:  fmt.Sprintf("relation %q", rel.Name),
			pos:    pos,
			end:    end,
		})
	}
	return members
}

// validateEntityStructNames reports, for one entity, spec §5.6's
// field/method collision (a field whose Go name collides with a method the
// generator emits on that struct) and the Field/Relation collision
// internal/parse cannot see (entityMember's doc comment). Field/Field
// collisions are internal/parse's own job (checkFieldCollisions) and are
// deliberately not repeated here: a schema with one never reaches this
// package, because internal/parse returns a nil *ir.Schema alongside its own
// diagnostic for it (see internal/parse/parse.go's Parse).
func validateEntityStructNames(e *ir.Entity, file string, diags *diag.Diagnostics) {
	members := entityMembers(e)
	seen := make(map[string]entityMember, len(members))
	for _, m := range members {
		if prior, ok := seen[m.goName]; ok {
			diags.Add(diag.Diagnostic{
				Severity:  diag.Error,
				File:      file,
				Pos:       m.pos,
				EndColumn: m.end.Column,
				Message:   fmt.Sprintf("%s and %s of entity %q both produce Go identifier %q", prior.label, m.label, e.Name, m.goName),
				Hint:      "rename one of them so their generated Go identifiers don't collide (spec §5.6)",
			})
		} else {
			seen[m.goName] = m
		}
		if reservedMethodNameSet[m.goName] {
			diags.Add(diag.Diagnostic{
				Severity:  diag.Error,
				File:      file,
				Pos:       m.pos,
				EndColumn: m.end.Column,
				Message:   fmt.Sprintf("%s of entity %q has Go name %q, which collides with a method the generator emits on that struct", m.label, e.Name, m.goName),
				Hint:      fmt.Sprintf("rename it; reserved method names: %s (spec §5.6)", strings.Join(reservedMethodNames, ", ")),
			})
		}
	}
}

// packageDecl is one top-level Go type name the generated model package will
// declare for the whole schema: an entity's own struct, or one enum field's
// generated enum type (Field.EnumGoType).
//
// Collected across every entity in schema.Entities -- sorted by name, an
// invariant ir.Schema.Freeze already checked before this package ever runs,
// never a map -- so a collision between two entities, two enum types, or an
// entity and an enum type (all of which, living in the one model package
// spec §6.1 describes, would be two conflicting top-level type declarations)
// is caught regardless of which entities produced them.
type packageDecl struct {
	goName   string
	kind     string // "entity" or "enum type"
	label    string
	pos, end source.Pos
}

// schemaPackageDecls returns one packageDecl per entity plus one per enum
// field, in schema.Entities order (Fields within an entity walked in
// declaration order) -- deterministic for a given schema, since neither loop
// ranges a map.
func schemaPackageDecls(schema *ir.Schema) []packageDecl {
	var decls []packageDecl
	for _, e := range schema.Entities {
		var pos, end source.Pos
		// ir.Entity carries no position of its own: spec §2.2 defines Entity
		// without one, consistent with source.At's own doc comment that it
		// is "applied selectively, to the leaves validation actually
		// blames: identifiers, enum members, sort keys" -- an entity's own
		// name is not among them. Its primary key field is the closest
		// genuine anchor available: always present (ir.Schema.Freeze
		// requires exactly one PK per entity) and always inside the right
		// entity's own `fields:` block, even though the diagnostic is about
		// the entity's name, not the PK field's.
		if e.PK != nil {
			pos, end = e.PK.Name.Pos, e.PK.Name.End
		}
		decls = append(decls, packageDecl{goName: e.GoName, kind: "entity", label: e.Name, pos: pos, end: end})
		for _, f := range e.Fields {
			if f.Type != ir.FieldTypeEnum {
				continue
			}
			decls = append(decls, packageDecl{
				goName: f.EnumGoType,
				kind:   "enum type",
				label:  e.Name + "." + f.Name.Value,
				pos:    f.Name.Pos,
				end:    f.Name.End,
			})
		}
	}
	return decls
}

// validatePackageNames reports spec §5.6's package-wide collisions: two
// entities whose Go names collide, and an enum field's generated type name
// colliding with another entity's or another enum field's.
func validatePackageNames(schema *ir.Schema, file string, diags *diag.Diagnostics) {
	decls := schemaPackageDecls(schema)
	seen := make(map[string]packageDecl, len(decls))
	for _, d := range decls {
		prior, ok := seen[d.goName]
		if !ok {
			seen[d.goName] = d
			continue
		}
		diags.Add(diag.Diagnostic{
			Severity:  diag.Error,
			File:      file,
			Pos:       d.pos,
			EndColumn: d.end.Column,
			Message:   fmt.Sprintf("%s %q and %s %q both produce Go identifier %q", prior.kind, prior.label, d.kind, d.label, d.goName),
			Hint:      "rename one of them so their generated Go identifiers don't collide (spec §5.6)",
		})
	}
}
