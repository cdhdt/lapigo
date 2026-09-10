package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// The cases below are named after spec §6.5's "Question 1" table rows, in
// the table's own order, checked one row at a time before the order-matters
// tests further down combine them.

func TestField_CreateInputPresence_PK(t *testing.T) {
	f := &Field{Name: source.Bare("id"), PK: true}
	if got := f.CreateInputPresence(); got != PresenceAbsent {
		t.Errorf("CreateInputPresence() = %v, want PresenceAbsent", got)
	}
}

func TestField_CreateInputPresence_Version(t *testing.T) {
	f := &Field{Name: source.Bare("version"), Version: true}
	if got := f.CreateInputPresence(); got != PresenceAbsent {
		t.Errorf("CreateInputPresence() = %v, want PresenceAbsent", got)
	}
}

func TestField_CreateInputPresence_ReadOnly(t *testing.T) {
	f := &Field{Name: source.Bare("slug"), ReadOnly: true}
	if got := f.CreateInputPresence(); got != PresenceHidden {
		t.Errorf("CreateInputPresence() = %v, want PresenceHidden", got)
	}
}

func TestField_CreateInputPresence_Immutable(t *testing.T) {
	f := &Field{Name: source.Bare("author"), Immutable: true}
	if got := f.CreateInputPresence(); got != PresenceVisible {
		t.Errorf("CreateInputPresence() = %v, want PresenceVisible", got)
	}
}

func TestField_CreateInputPresence_Plain(t *testing.T) {
	f := &Field{Name: source.Bare("title")}
	if got := f.CreateInputPresence(); got != PresenceVisible {
		t.Errorf("CreateInputPresence() = %v, want PresenceVisible", got)
	}
}

func TestField_UpdateInputPresence_PK(t *testing.T) {
	f := &Field{Name: source.Bare("id"), PK: true}
	if got := f.UpdateInputPresence(); got != PresenceAbsent {
		t.Errorf("UpdateInputPresence() = %v, want PresenceAbsent", got)
	}
}

func TestField_UpdateInputPresence_Version(t *testing.T) {
	f := &Field{Name: source.Bare("version"), Version: true}
	if got := f.UpdateInputPresence(); got != PresenceAbsent {
		t.Errorf("UpdateInputPresence() = %v, want PresenceAbsent", got)
	}
}

func TestField_UpdateInputPresence_ReadOnly(t *testing.T) {
	f := &Field{Name: source.Bare("slug"), ReadOnly: true}
	if got := f.UpdateInputPresence(); got != PresenceHidden {
		t.Errorf("UpdateInputPresence() = %v, want PresenceHidden", got)
	}
}

// TestField_UpdateInputPresence_Immutable is the row the old single
// subtraction table got right on its own: an immutable field is present on
// create and gone from update.
func TestField_UpdateInputPresence_Immutable(t *testing.T) {
	f := &Field{Name: source.Bare("author"), Immutable: true}
	if got := f.UpdateInputPresence(); got != PresenceAbsent {
		t.Errorf("UpdateInputPresence() = %v, want PresenceAbsent", got)
	}
}

func TestField_UpdateInputPresence_Plain(t *testing.T) {
	f := &Field{Name: source.Bare("title")}
	if got := f.UpdateInputPresence(); got != PresenceVisible {
		t.Errorf("UpdateInputPresence() = %v, want PresenceVisible", got)
	}
}

// The following three tests pin the FIRST-MATCH order itself, not just each
// row in isolation: a Field carrying two of the flags at once must resolve
// to whichever row comes first in spec §6.5's table, not the more
// "specific-looking" one. Getting this order wrong is exactly what produced
// the blocking review finding for {required: true, immutable: true} (see
// TestField_CreateInputPresence_RequiredImmutableCombination below), so the
// same discipline is applied to every pair of flags that could plausibly be
// set together on a hand-built or buggily-resolved Field.

func TestField_CreateInputPresence_PKBeatsReadOnly(t *testing.T) {
	f := &Field{Name: source.Bare("id"), PK: true, ReadOnly: true}
	if got := f.CreateInputPresence(); got != PresenceAbsent {
		t.Errorf("CreateInputPresence() = %v, want PresenceAbsent (pk row must win over readonly)", got)
	}
}

func TestField_CreateInputPresence_VersionBeatsImmutable(t *testing.T) {
	f := &Field{Name: source.Bare("version"), Version: true, Immutable: true}
	if got := f.CreateInputPresence(); got != PresenceAbsent {
		t.Errorf("CreateInputPresence() = %v, want PresenceAbsent (version row must win over immutable)", got)
	}
}

func TestField_CreateInputPresence_ReadOnlyBeatsImmutable(t *testing.T) {
	f := &Field{Name: source.Bare("slug"), ReadOnly: true, Immutable: true}
	if got := f.CreateInputPresence(); got != PresenceHidden {
		t.Errorf("CreateInputPresence() = %v, want PresenceHidden (readonly row must win over immutable)", got)
	}
}

// TestField_IsMandatory pins spec §6.5's "Question 2": mandatory exactly
// when the field is required (not nullable) and carries no default.
func TestField_IsMandatory(t *testing.T) {
	tests := []struct {
		name string
		f    *Field
		want bool
	}{
		{"required, no default", &Field{Name: source.Bare("title"), Nullable: false}, true},
		{"required, with default", &Field{Name: source.Bare("status"), Nullable: false, Default: &DefaultValue{Kind: DefaultLiteral, Literal: "draft"}}, false},
		{"nullable, no default", &Field{Name: source.Bare("bio"), Nullable: true}, false},
		{"nullable, with default", &Field{Name: source.Bare("bio"), Nullable: true, Default: &DefaultValue{Kind: DefaultNow}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.IsMandatory(); got != tt.want {
				t.Errorf("IsMandatory() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestField_RequiredImmutableCombination is the review finding this whole
// accessor exists to make impossible to get wrong again: a field carrying
// both `required: true` (Nullable: false) and `immutable: true` must be
// present AND mandatory on create, and wholly absent from update. The old
// single subtraction table matched the immutable row and answered
// "optional on create" instead -- a NOT NULL column with no default,
// silently omitted by a client, inserted NULL, and 23502 with no mapping in
// spec §6.7's table.
func TestField_RequiredImmutableCombination(t *testing.T) {
	f := &Field{Name: source.Bare("author"), Nullable: false, Immutable: true}

	if got := f.CreateInputPresence(); got != PresenceVisible {
		t.Errorf("CreateInputPresence() = %v, want PresenceVisible", got)
	}
	if !f.IsMandatory() {
		t.Error("IsMandatory() = false, want true: required with no default must be mandatory")
	}
	if got := f.UpdateInputPresence(); got != PresenceAbsent {
		t.Errorf("UpdateInputPresence() = %v, want PresenceAbsent", got)
	}
}

// TestFieldPresence_String_Unknown asserts the exact placeholder, matching
// the discipline already applied to FieldType, FilterOp, EndpointKind and
// DefaultKind: a got == "" or got != "" assertion would not catch a
// wrong-but-plausible string for an out-of-range value.
func TestFieldPresence_String_Unknown(t *testing.T) {
	p := FieldPresence(99)
	const want = "FieldPresence(99)"
	if got := p.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestFieldPresence_String_KnownValues(t *testing.T) {
	tests := []struct {
		p    FieldPresence
		want string
	}{
		{PresenceAbsent, "absent"},
		{PresenceHidden, "hidden"},
		{PresenceVisible, "visible"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.p.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}
