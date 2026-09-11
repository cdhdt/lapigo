package gen

import (
	"fmt"
	"strings"

	"github.com/cdhdt/lapigo/internal/ir"
)

// This file is step 7's other half of spec §6.5 (issue #28): the generated
// Validate() error method on CreateInput and UpdateInput. Step 6 (input.go)
// built the structs and Optional[T] because hooks could not type-check
// without them; nothing here changes either. What this file adds is the
// method that actually enforces spec §6.5's "Question 2" (which members are
// mandatory), the Go half of §3.1's `max:` check, enum membership, and
// §6.6's rule that an explicit null is rejected against a member whose
// column does not accept NULL.
//
// validateMember mirrors input.go's inputMember: one field's contribution to
// a generated Validate method, computed once here so the template only
// selects among a small, enumerated set of shapes (spec §5.1) and never
// re-derives "is this member mandatory" or "does this column accept null"
// on its own. Every message a check can emit is a complete literal string,
// computed here at generation time from the schema's own Max and
// EnumValues -- never built with fmt.Sprintf in the generated code -- so
// the generated file needs no import beyond what CreateInput/UpdateInput
// already need.
type validateMember struct {
	GoName string // input struct member name, e.g. "Title"
	Column string // wire name; the map key a failure is filed under (spec §6.9.2)

	// IsCreate is true for a CreateInput member and false for an UpdateInput
	// member. It selects which "missing" test spec §6.5 prescribes:
	// !Present() || IsNull() on CreateInput; IsNull() alone on UpdateInput,
	// where an absent key means "unchanged" (spec §6.6).
	IsCreate bool

	// Mandatory is f.IsMandatory(). Asking it is safe here specifically
	// because validateMembersFor only ever builds a validateMember for a
	// field whose presence on THIS input type is ir.PresenceVisible --
	// IsMandatory's own doc comment says it means something only for such a
	// member, and is moot (not wrong) for anything else. A Hidden
	// (readonly) member's value is set by a hook, and a hook runs after
	// Validate in the fixed decode-Validate-transaction-BeforeX order, so
	// checking mandatoriness on it here would reject a request before the
	// hook ever got the chance to supply the value.
	Mandatory bool

	// NullCheck is true for a member that is optional (not Mandatory) but
	// whose column does not accept NULL: `required: true` with a
	// `default:` (spec §3.1, §6.5) is exactly that shape -- absence is
	// fine, because the default supplies the value at insert, but an
	// explicit null is not, because the column is NOT NULL. A Mandatory
	// member's own null case is already covered by its "missing" test
	// above (a null there means "required", not "must not be null"), so
	// the two are mutually exclusive by construction.
	NullCheck bool

	// HasMax, Max and MaxMessage carry spec §3.1's Go half of `max:`: the
	// database gets varchar(n) (internal/ddl), and this is the check that
	// keeps a client from ever discovering the mismatch as an unmapped
	// 500 from Postgres (spec §6.7).
	HasMax     bool
	Max        int
	MaxMessage string

	// IsEnum, EnumGoType, EnumValues and EnumMessage carry the enum
	// membership check (spec §6.5, §6.7): the CHECK constraint is the
	// database's backstop, not the first line, because a violation there is
	// a 500 through §6.7's mapping, and §6.7's whole point is that a client
	// error must not become one. EnumValues lets the template build a
	// switch over the field's own generated constants
	// (model_entity.tmpl's EnumGoType+GoName), so a schema value is never
	// compared as a bare string at runtime.
	IsEnum      bool
	EnumGoType  string
	EnumValues  []ir.EnumValue
	EnumMessage string
}

// createValidateMembers returns e's CreateInput members that Validate must
// check, in declaration order (spec §5.3: never a map).
func createValidateMembers(e *ir.Entity) []validateMember {
	return validateMembersFor(e, (*ir.Field).CreateInputPresence, true)
}

// updateValidateMembers returns e's UpdateInput members that Validate must
// check, in declaration order.
func updateValidateMembers(e *ir.Entity) []validateMember {
	return validateMembersFor(e, (*ir.Field).UpdateInputPresence, false)
}

// validateMembersFor walks e.Fields once, in declaration order, and keeps
// only the fields whose presence on this input type (per presenceOf --
// ir.Field.CreateInputPresence or UpdateInputPresence) is
// ir.PresenceVisible: an Absent field (pk, version, an immutable field on
// UpdateInput) has no struct member to check, and a Hidden (readonly) field
// has a member but it is never decoded from a request -- DisallowUnknownFields
// rejects the key -- so nothing about it is ever "missing" or "null" in the
// sense Validate exists to catch (spec §6.5's "off the wire, not out of the
// struct").
//
// A field that survives that filter but has nothing at all for Validate to
// say -- optional, nullable, not a string with `max:`, not an enum -- is
// still dropped: the returned slice, and therefore the generated method,
// carries no dead branch for a member that can never fail.
func validateMembersFor(e *ir.Entity, presenceOf func(*ir.Field) ir.FieldPresence, isCreate bool) []validateMember {
	var out []validateMember
	for _, f := range e.Fields {
		if presenceOf(f) != ir.PresenceVisible {
			continue
		}

		m := validateMember{
			GoName:    f.GoName,
			Column:    f.Column,
			IsCreate:  isCreate,
			Mandatory: f.IsMandatory(),
		}
		m.NullCheck = !m.Mandatory && !f.Nullable

		if f.Type == ir.FieldTypeString && f.Max != nil {
			m.HasMax = true
			m.Max = *f.Max
			m.MaxMessage = fmt.Sprintf("must be at most %d characters", *f.Max)
		}
		if f.Type == ir.FieldTypeEnum {
			m.IsEnum = true
			m.EnumGoType = f.EnumGoType
			m.EnumValues = f.EnumValues
			m.EnumMessage = "must be one of: " + enumValueList(f.EnumValues)
		}

		if !m.Mandatory && !m.NullCheck && !m.HasMax && !m.IsEnum {
			continue
		}
		out = append(out, m)
	}
	return out
}

// enumValueList renders values' own written names (never their Go
// identifiers) joined for a client-facing message, in the schema's own
// declaration order -- the same order model_entity.tmpl emits their
// generated constants in.
func enumValueList(values []ir.EnumValue) string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = v.Name.Value
	}
	return strings.Join(names, ", ")
}
