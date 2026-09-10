package gen

import (
	"fmt"

	"github.com/cdhdt/lapigo/internal/ir"
)

// This file is step 6's other half of the pipeline (spec §6.5), added once
// measurement showed the hooks package cannot type-check without it: §6.3's
// BeforeCreate/BeforeUpdate name *model.<Entity>CreateInput and
// *model.<Entity>UpdateInput, so those two struct types are hooks'
// prerequisite, not step 7's consequence (spec §10's amended step 6 row,
// CLAUDE.md's build-order table).
//
// Only the structs. Validate() error -- the method that actually enforces
// spec §6.5's "Question 2", mandatoriness -- stays step 7's (issue #28), and
// so does everything else step 7 owns: the store, the cursor codec, the
// keyset predicate. Nothing here needs any of them: a struct of Optional[T]
// members type-checks on its own.

// inputMember is one field's contribution to a generated input struct: its
// Go name, the T of its Optional[T] (spec §6.5's ValueGoType, never GoType --
// see ir.Field.ValueGoType's own doc comment for why a pointer form would be
// wrong here), and its complete struct tag, already rendered.
type inputMember struct {
	GoName      string
	ValueGoType string
	Tag         string // a full backticked tag, e.g. "`json:\"title\"`" or "`json:\"-\"`"
}

// createInputMembers returns e's CreateInput members, in declaration order
// (spec §5.3: never a map, and the emitted order is part of the output's
// bytes).
func createInputMembers(e *ir.Entity) ([]inputMember, error) {
	return inputMembersFor(e, (*ir.Field).CreateInputPresence)
}

// updateInputMembers returns e's UpdateInput members, in declaration order.
func updateInputMembers(e *ir.Entity) ([]inputMember, error) {
	return inputMembersFor(e, (*ir.Field).UpdateInputPresence)
}

// inputMembersFor walks e.Fields once, in declaration order, and asks
// presenceOf -- ir.Field.CreateInputPresence or UpdateInputPresence,
// selected by the caller -- spec §6.5's "Question 1" for each. This is the
// one place that question is asked in this package: §5.1 rejects
// re-deriving the same first-match table as a boolean chain in a template,
// which is exactly the shape a blocking review finding (spec §13) already
// caught getting the order wrong once.
//
// A member absent from this input type (ir.PresenceAbsent) contributes
// nothing -- pk, version, or an immutable field on UpdateInput. A hidden one
// (ir.PresenceHidden, a readonly field) still gets a member, tagged
// `json:"-"` so a hook can set it through Optional[T] while
// DisallowUnknownFields rejects the same key from a request (spec §6.5,
// "off the wire, not out of the struct"). A visible one gets the same
// json tag every other place a field is named on the wire uses (jsonTag,
// gen.go).
func inputMembersFor(e *ir.Entity, presenceOf func(*ir.Field) ir.FieldPresence) ([]inputMember, error) {
	var out []inputMember
	for _, f := range e.Fields {
		switch presenceOf(f) {
		case ir.PresenceAbsent:
			continue
		case ir.PresenceHidden:
			out = append(out, inputMember{
				GoName:      f.GoName,
				ValueGoType: f.ValueGoType(),
				Tag:         "`json:\"-\"`",
			})
		case ir.PresenceVisible:
			tag, err := jsonTag(f.Column)
			if err != nil {
				return nil, fmt.Errorf("gen: entity %q, field %q: %w", e.Name, f.Name.Value, err)
			}
			out = append(out, inputMember{
				GoName:      f.GoName,
				ValueGoType: f.ValueGoType(),
				Tag:         tag,
			})
		default:
			// ir.FieldPresence is a closed enumeration; every value it can
			// hold is listed above. An out-of-range one (a bug in whoever
			// built the Field, since the IR only ever produces the three
			// known presences) is an error here rather than a silently
			// dropped or silently visible member.
			return nil, fmt.Errorf("gen: entity %q, field %q: unknown %s", e.Name, f.Name.Value, presenceOf(f))
		}
	}
	return out, nil
}
