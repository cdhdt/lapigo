package ir

import "fmt"

// FieldPresence describes how a Field appears as a struct member of one
// generated input type (CreateInput or UpdateInput), per spec §6.5's
// "Question 1" table.
type FieldPresence int

const (
	// PresenceAbsent means the field has no member on this input type at
	// all: nothing on this write path can ever set it from a request.
	PresenceAbsent FieldPresence = iota
	// PresenceHidden means the field has a member on this input type, but
	// it is tagged json:"-" and never reaches the wire: only a hook can set
	// it, through Optional[T]'s Set/SetNull (spec §6.5, "off the wire, not
	// out of the struct").
	PresenceHidden
	// PresenceVisible means the field has a member on this input type that
	// a client may set over the wire.
	PresenceVisible
)

// String returns a lowercase name for p, matching the pattern of
// FieldType.String, FilterOp.String, EndpointKind.String and
// DefaultKind.String: an out-of-range value gets an explicit, self-naming
// placeholder rather than a value that looks like a real one.
func (p FieldPresence) String() string {
	switch p {
	case PresenceAbsent:
		return "absent"
	case PresenceHidden:
		return "hidden"
	case PresenceVisible:
		return "visible"
	default:
		return fmt.Sprintf("FieldPresence(%d)", int(p))
	}
}

// CreateInputPresence reports how f appears on the generated CreateInput
// struct, per spec §6.5's "Question 1" table, read as FIRST-MATCH:
//
//	pk              -> absent
//	version: true   -> absent
//	readonly: true  -> hidden (member, json:"-")
//	immutable: true -> present
//	anything else   -> present
//
// The order is the table's own and is load-bearing, not incidental: a field
// carrying both `required: true` and `immutable: true` must still land on
// the "anything else"/`immutable:` presence answer (present), never on a
// row a later, more specific-looking condition might suggest. Modelled as a
// switch over the table's rows in the table's order — never as the boolean
// chain (`!f.PK && f.Default == nil && !f.ReadOnly`) spec §5.1 rejects in a
// template — so a reordering that breaks the table is a diff to this
// function, not a fresh derivation at every call site.
//
// See UpdateInputPresence for the other input type's answer to the same
// question, and IsMandatory for "Question 2", which this method does not
// answer: presence and requiredness are independent facts (spec §6.5).
func (f *Field) CreateInputPresence() FieldPresence {
	switch {
	case f.PK:
		return PresenceAbsent
	case f.Version:
		return PresenceAbsent
	case f.ReadOnly:
		return PresenceHidden
	default:
		// Covers both the `immutable: true` row and "anything else": both
		// answer PresenceVisible for CreateInput, and the table gives them
		// no distinguishing behavior here — the immutable/plain distinction
		// only shows up in UpdateInputPresence.
		return PresenceVisible
	}
}

// UpdateInputPresence reports how f appears on the generated UpdateInput
// struct, per spec §6.5's "Question 1" table, read FIRST-MATCH exactly like
// CreateInputPresence:
//
//	pk              -> absent
//	version: true   -> absent
//	readonly: true  -> hidden (member, json:"-")
//	immutable: true -> absent
//	anything else   -> present
//
// This is the row that a subtraction-based design (revision 1) could not
// express without a second, drifting copy of "no default"/"readonly"
// reasoning: `immutable: true` is the one flag whose answer differs between
// the two input types, which is exactly why input projection is asked as
// two separate per-type questions rather than one shared table with a
// column each (spec §6.5's own framing).
func (f *Field) UpdateInputPresence() FieldPresence {
	switch {
	case f.PK:
		return PresenceAbsent
	case f.Version:
		return PresenceAbsent
	case f.ReadOnly:
		return PresenceHidden
	case f.Immutable:
		return PresenceAbsent
	default:
		return PresenceVisible
	}
}

// IsMandatory reports spec §6.5's "Question 2": whether a client must send
// this member when it is on the wire at all. A member is mandatory exactly
// when its field is required (Nullable is false — buildField sets
// Nullable = !required && !pk, so a non-pk field's Nullable already carries
// the `required:` fact) and carries no `default:`; every other member is
// optional, because either `default:` supplies the value at insert or the
// column accepts NULL.
//
// IsMandatory is asked identically of both input types (spec §6.5: "Asked
// only of a member that is on the wire" — but the answer does not depend on
// which wire), so there is one method, not
// CreateInputIsMandatory/UpdateInputIsMandatory. It means something only for
// a member CreateInputPresence or UpdateInputPresence reports
// PresenceVisible for; calling it on a pk, version or readonly field is not
// wrong, just moot, since no such member is ever decoded from a request in
// the first place — DisallowUnknownFields rejects the key, or there is no
// member at all.
func (f *Field) IsMandatory() bool {
	return !f.Nullable && f.Default == nil
}
