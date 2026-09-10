package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

func TestField_IsSortEligible_Nullable(t *testing.T) {
	f := &Field{Name: source.Bare("created_at"), Type: FieldTypeTimestamp, Nullable: true}

	const want = "field is nullable: a keyset comparison against NULL yields unknown, silently dropping every row after the seek"
	if got := f.IsSortEligible(); got != want {
		t.Errorf("IsSortEligible() = %q, want %q", got, want)
	}
}

func TestField_IsSortEligible_Decimal(t *testing.T) {
	f := &Field{Name: source.Bare("price"), Type: FieldTypeDecimal}

	const want = "field type is decimal: pgtype.Numeric marshals to a bare JSON number, which a generic decoder routes through float64, defeating the exactness the type exists for"
	if got := f.IsSortEligible(); got != want {
		t.Errorf("IsSortEligible() = %q, want %q", got, want)
	}
}

func TestField_IsSortEligible_JSON(t *testing.T) {
	f := &Field{Name: source.Bare("meta"), Type: FieldTypeJSON}

	const want = "field type is json: not ordered, so it cannot serve as a keyset comparison"
	if got := f.IsSortEligible(); got != want {
		t.Errorf("IsSortEligible() = %q, want %q", got, want)
	}
}

func TestField_IsSortEligible_Eligible(t *testing.T) {
	tests := []struct {
		name string
		f    *Field
	}{
		{"non-nullable timestamp", &Field{Name: source.Bare("created_at"), Type: FieldTypeTimestamp}},
		{"non-nullable uuid pk", &Field{Name: source.Bare("id"), Type: FieldTypeUUID, PK: true}},
		{"non-nullable string", &Field{Name: source.Bare("slug"), Type: FieldTypeString}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.IsSortEligible(); got != "" {
				t.Errorf("IsSortEligible() = %q, want the empty string", got)
			}
		})
	}
}

// TestField_IsSortEligible_UnknownTypeHasNoDefaultBranch pins that an
// out-of-range FieldType is rejected, not silently treated as eligible.
// IsSortEligible's switch previously had no default branch, so FieldType(99)
// fell through both cases and came out eligible.
func TestField_IsSortEligible_UnknownTypeHasNoDefaultBranch(t *testing.T) {
	f := &Field{Name: source.Bare("mystery"), Type: FieldType(99)}

	if got := f.IsSortEligible(); got == "" {
		t.Error(`IsSortEligible() = "" (eligible) for an unknown FieldType, want a reason`)
	}
}

func TestField_GoType(t *testing.T) {
	tests := []struct {
		name string
		f    *Field
		want string
	}{
		{"non-nullable scalar defers to FieldType", &Field{Type: FieldTypeInt}, "int32"},
		{"nullable scalar defers to FieldType", &Field{Type: FieldTypeInt, Nullable: true}, "*int32"},
		{"non-nullable enum uses EnumGoType, not FieldType's placeholder",
			&Field{Type: FieldTypeEnum, EnumGoType: "ArticleStatus"}, "ArticleStatus"},
		{"nullable enum uses *EnumGoType, not *string",
			&Field{Type: FieldTypeEnum, EnumGoType: "ArticleStatus", Nullable: true}, "*ArticleStatus"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.GoType(); got != tt.want {
				t.Errorf("GoType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestField_ValueGoType pins ValueGoType against GoType's own cases: same
// Field values, but nullability must never surface in the result, because
// ValueGoType answers for Optional[T]'s T (spec §6.5), not for the
// marshalled model. A nullable and a non-nullable field of the same
// FieldType must produce the identical literal.
func TestField_ValueGoType(t *testing.T) {
	tests := []struct {
		name string
		f    *Field
		want string
	}{
		{"nullable scalar strips nullability", &Field{Type: FieldTypeString, Nullable: true}, "string"},
		{"non-nullable scalar, same FieldType, identical literal", &Field{Type: FieldTypeString}, "string"},
		{"enum uses EnumGoType, not FieldType's placeholder",
			&Field{Type: FieldTypeEnum, EnumGoType: "ArticleStatus"}, "ArticleStatus"},
		{"nullable enum stays bare EnumGoType, never *EnumGoType",
			&Field{Type: FieldTypeEnum, EnumGoType: "ArticleStatus", Nullable: true}, "ArticleStatus"},
		{"belongsTo foreign key column uses the FK scalar's FieldTypeUUID placeholder",
			&Field{Type: FieldTypeUUID, Nullable: true}, "pgtype.UUID"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.ValueGoType(); got != tt.want {
				t.Errorf("ValueGoType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestField_PgType(t *testing.T) {
	max200 := 200
	tests := []struct {
		name string
		f    *Field
		want string
	}{
		{"plain scalar defers to FieldType", &Field{Type: FieldTypeText}, "text"},
		{"string without max stays text", &Field{Type: FieldTypeString}, "text"},
		{"string with max upgrades to varchar(n)", &Field{Type: FieldTypeString, Max: &max200}, "varchar(200)"},
		{"enum stays text regardless of EnumGoType", &Field{Type: FieldTypeEnum, EnumGoType: "ArticleStatus"}, "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.f.PgType(); got != tt.want {
				t.Errorf("PgType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultValue_Now(t *testing.T) {
	d := DefaultValue{Kind: DefaultNow}
	if d.Kind != DefaultNow {
		t.Errorf("Kind = %v, want DefaultNow", d.Kind)
	}
}

func TestDefaultValue_Literal(t *testing.T) {
	d := DefaultValue{Kind: DefaultLiteral, Literal: "draft"}
	if d.Literal != "draft" {
		t.Errorf("Literal = %q, want %q", d.Literal, "draft")
	}
}

func TestDefaultKind_String(t *testing.T) {
	tests := []struct {
		kind DefaultKind
		want string
	}{
		{DefaultNow, "now"},
		{DefaultUUID, "uuid"},
		{DefaultLiteral, "literal"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestDefaultKind_String_Unknown asserts the exact placeholder, matching the
// same discipline applied to FieldType, FilterOp and EndpointKind: a
// got == "" assertion would not catch a wrong-but-plausible string.
func TestDefaultKind_String_Unknown(t *testing.T) {
	k := DefaultKind(99)
	const want = "DefaultKind(99)"
	if got := k.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
