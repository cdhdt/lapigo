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

// TestFieldType_GoType covers every row of spec §3.2, both nullable and not.
// Nullable fields use the pointer form so that "absent" and "null" are
// distinguishable.
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
		{"enum", FieldTypeEnum, "string", "*string"},
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

func TestFieldType_String_Unknown(t *testing.T) {
	var typ FieldType = 999
	got := typ.String()
	if got == "" {
		t.Error("String() on an unknown FieldType returned empty string, want a diagnostic placeholder")
	}
}
