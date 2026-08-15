package ir

import "testing"

// TestFieldType_PgType covers every row of spec §3.2. PgType reports the base
// Postgres column type; field-level modifiers (a string's max length, an
// enum's CHECK constraint) are resolved elsewhere, not by FieldType.
func TestFieldType_PgType(t *testing.T) {
	tests := []struct {
		yaml string
		typ  FieldType
		want string
	}{
		{"uuid", FieldTypeUUID, "uuid"},
		{"string", FieldTypeString, "text"},
		{"text", FieldTypeText, "text"},
		{"int", FieldTypeInt, "integer"},
		{"bigint", FieldTypeBigint, "bigint"},
		{"float", FieldTypeFloat, "double precision"},
		{"decimal", FieldTypeDecimal, "numeric"},
		{"bool", FieldTypeBool, "boolean"},
		{"timestamp", FieldTypeTimestamp, "timestamptz"},
		{"date", FieldTypeDate, "date"},
		{"json", FieldTypeJSON, "jsonb"},
		{"enum", FieldTypeEnum, "text"},
	}
	for _, tt := range tests {
		t.Run(tt.yaml, func(t *testing.T) {
			if got := tt.typ.PgType(); got != tt.want {
				t.Errorf("PgType() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFieldType_GoType covers every row of spec §3.2 except enum, which
// FieldType deliberately refuses to answer for — see
// TestFieldType_GoType_Enum below and Field.GoType in field_test.go, which is
// what actually resolves an enum's Go type.
func TestFieldType_GoType(t *testing.T) {
	tests := []struct {
		yaml         string
		typ          FieldType
		wantNonNull  string
		wantNullable string
	}{
		{"uuid", FieldTypeUUID, "pgtype.UUID", "*pgtype.UUID"},
		{"string", FieldTypeString, "string", "*string"},
		{"text", FieldTypeText, "string", "*string"},
		{"int", FieldTypeInt, "int32", "*int32"},
		{"bigint", FieldTypeBigint, "int64", "*int64"},
		{"float", FieldTypeFloat, "float64", "*float64"},
		{"decimal", FieldTypeDecimal, "pgtype.Numeric", "*pgtype.Numeric"},
		{"bool", FieldTypeBool, "bool", "*bool"},
		{"timestamp", FieldTypeTimestamp, "time.Time", "*time.Time"},
		{"date", FieldTypeDate, "time.Time", "*time.Time"},
		{"json", FieldTypeJSON, "json.RawMessage", "*json.RawMessage"},
	}
	for _, tt := range tests {
		t.Run(tt.yaml+"/non-nullable", func(t *testing.T) {
			if got := tt.typ.GoType(false); got != tt.wantNonNull {
				t.Errorf("GoType(false) = %q, want %q", got, tt.wantNonNull)
			}
		})
		t.Run(tt.yaml+"/nullable", func(t *testing.T) {
			if got := tt.typ.GoType(true); got != tt.wantNullable {
				t.Errorf("GoType(true) = %q, want %q", got, tt.wantNullable)
			}
		})
	}
}

// TestFieldType_PgType_Unknown covers PgType's own out-of-range branch,
// distinct from String's: a bad PgType must not silently emit a real-looking
// column type either.
func TestFieldType_PgType_Unknown(t *testing.T) {
	typ := FieldType(99)
	const want = "<unknown FieldType 99>"
	if got := typ.PgType(); got != want {
		t.Errorf("PgType() = %q, want %q", got, want)
	}
}

// TestFieldType_GoType_UnknownNonEnum covers goTypeBase's own out-of-range
// branch for a FieldType that is neither a known scalar nor FieldTypeEnum
// (which GoType intercepts before ever reaching goTypeBase).
func TestFieldType_GoType_UnknownNonEnum(t *testing.T) {
	typ := FieldType(99)
	const want = "<unknown FieldType 99>"
	if got := typ.GoType(false); got != want {
		t.Errorf("GoType(false) = %q, want %q", got, want)
	}
}

// TestFieldType_GoType_Enum pins the fix for the contradiction spec §2.2
// calls out: FieldType.GoType(true) used to return "*string" for an enum,
// while the Field that owns it needs "*ArticleStatus" — a bare FieldType has
// no entity context to produce that name from. Rather than return a
// plausible-looking wrong answer, FieldType.GoType now returns an explicitly
// unusable placeholder for FieldTypeEnum in both directions.
func TestFieldType_GoType_Enum(t *testing.T) {
	const want = "<invalid: FieldType.GoType cannot answer for an enum, use Field.GoType>"
	if got := FieldTypeEnum.GoType(false); got != want {
		t.Errorf("GoType(false) = %q, want %q", got, want)
	}
	if got := FieldTypeEnum.GoType(true); got != want {
		t.Errorf("GoType(true) = %q, want %q", got, want)
	}
}

// TestFieldType_String covers the YAML keyword each FieldType round-trips
// to, used in diagnostics by later packages.
func TestFieldType_String(t *testing.T) {
	tests := []struct {
		typ  FieldType
		want string
	}{
		{FieldTypeUUID, "uuid"},
		{FieldTypeString, "string"},
		{FieldTypeText, "text"},
		{FieldTypeInt, "int"},
		{FieldTypeBigint, "bigint"},
		{FieldTypeFloat, "float"},
		{FieldTypeDecimal, "decimal"},
		{FieldTypeBool, "bool"},
		{FieldTypeTimestamp, "timestamp"},
		{FieldTypeDate, "date"},
		{FieldTypeJSON, "json"},
		{FieldTypeEnum, "enum"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.typ.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFieldType_String_Unknown asserts the exact placeholder, not merely
// that it is non-empty. "string" is itself non-empty, so a got == ""
// assertion would not have caught FieldType.String() wrongly reporting an
// out-of-range value as a legitimate type keyword.
func TestFieldType_String_Unknown(t *testing.T) {
	typ := FieldType(99)
	const want = "FieldType(99)"
	if got := typ.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
