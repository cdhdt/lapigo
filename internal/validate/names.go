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

// IsReservedMethodName reports whether name is one of reservedMethodNames.
//
// Exported only for internal/gen's containment check (spec §5.6's own "what
// this does not yet check", closed by issue #28): "reservedMethodNames is
// deliberately a superset of what is emitted" is a claim, not yet
// mechanically enforced anywhere before that issue, and internal/gen is the
// one package positioned to enforce it -- it is the only place both halves
// (what a template actually emits, and what this list reserves) are ever
// in scope together. This does not expose reservedMethodNames itself: a
// membership query is all the containment check needs, and it is also all
// that keeps this package's own list private to the check that reads
// individual names, rather than a second copy of the slice living in gen.
func IsReservedMethodName(name string) bool {
	return reservedMethodNameSet[name]
}

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
		members = append(members, entityMember{
			goName: rel.GoName,
			label:  fmt.Sprintf("relation %q", rel.Name),
			pos:    rel.NameSpan.Start,
			end:    rel.NameSpan.End,
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

// packageDecl is one top-level Go declaration the generated model package
// will emit for the whole schema: an entity's own struct, one enum field's
// generated enum type (Field.EnumGoType), or one enum value's generated
// constant (Field.EnumGoType + EnumValue.GoName).
//
// The constant case is why this cannot be scoped any narrower than the whole
// package (review finding F1 on PR #39, issue #24): an earlier revision kept
// a separate, per-field-only check for enum-value collisions, justified by
// the claim that "an enum field's generated constants live under that
// field's own EnumGoType and are never mixed with another field's". That
// claim is false. A constant's name is EnumGoType + GoName, and EnumGoType
// is itself entityGoName + goName(fieldName) (internal/parse/field.go) --
// concatenating two variable-length prefixes is ambiguous, so two different
// fields' constants collide freely: field "state" with value "x_y" produces
// "Task"+"State"+"XY", and field "state_x" with value "y" produces
// "Task"+"StateX"+"Y" -- the same "TaskStateXY". Only comparing every
// constant against every other top-level declaration in the same flat
// namespace -- entities, enum types, and now enum constants together --
// catches that, a constant colliding with another field's enum type name, or
// a constant colliding with an entity's own struct name, uniformly with the
// within-field case ("in-progress"/"in_progress", issue #24's own example).
//
// Collected across every entity in schema.Entities -- sorted by name, an
// invariant ir.Schema.Freeze already checked before this package ever runs,
// never a map -- so a collision is caught regardless of which entities or
// fields produced the two colliding declarations.
type packageDecl struct {
	goName   string
	kind     string // "entity", "enum type", or "enum value"
	label    string
	pos, end source.Pos
}

// schemaPackageDecls returns one packageDecl per entity, one per enum field,
// and one per enum value, in schema.Entities order (Fields within an entity,
// and EnumValues within a field, walked in declaration order) --
// deterministic for a given schema, since no loop here ranges a map.
func schemaPackageDecls(schema *ir.Schema) []packageDecl {
	var decls []packageDecl
	for _, e := range schema.Entities {
		decls = append(decls, packageDecl{
			goName: e.GoName, kind: "entity", label: e.Name,
			pos: e.NameSpan.Start, end: e.NameSpan.End,
		})
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
			for _, v := range f.EnumValues {
				decls = append(decls, packageDecl{
					goName: f.EnumGoType + v.GoName,
					kind:   "enum value",
					label:  e.Name + "." + f.Name.Value + "." + v.Name.Value,
					pos:    v.Name.Pos,
					end:    v.Name.End,
				})
			}
		}
	}
	return decls
}

// validatePackageNames reports spec §5.6's package-wide collisions: two
// entities, two enum types, two enum constants, or any mix of the three,
// whose generated Go identifier collides -- see packageDecl's own doc
// comment for why an enum constant cannot be checked against anything
// narrower than this whole-package namespace.
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
