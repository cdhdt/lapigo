package ir

import "fmt"

// FieldType is a field's scalar type, resolved from the YAML `type:` keyword
// during parsing. It is a closed enumeration rather than a string so that the
// type mapping in spec §3.2 — which Postgres column, which Go type — is a
// switch a compiler checks for exhaustiveness, not a string a template would
// have to compare against typos.
type FieldType int

const (
	FieldTypeUUID FieldType = iota
	FieldTypeString
	FieldTypeText
	FieldTypeInt
	FieldTypeBigint
	FieldTypeFloat
	FieldTypeDecimal
	FieldTypeBool
	FieldTypeTimestamp
	FieldTypeDate
	FieldTypeJSON
	FieldTypeEnum
)

// String returns the YAML `type:` keyword t was parsed from. Used in
// diagnostics; not used for the Postgres or Go mappings, which are PgType
// and GoType.
func (t FieldType) String() string {
	switch t {
	case FieldTypeUUID:
		return "uuid"
	case FieldTypeString:
		return "string"
	case FieldTypeText:
		return "text"
	case FieldTypeInt:
		return "int"
	case FieldTypeBigint:
		return "bigint"
	case FieldTypeFloat:
		return "float"
	case FieldTypeDecimal:
		return "decimal"
	case FieldTypeBool:
		return "bool"
	case FieldTypeTimestamp:
		return "timestamp"
	case FieldTypeDate:
		return "date"
	case FieldTypeJSON:
		return "json"
	case FieldTypeEnum:
		return "enum"
	default:
		return fmt.Sprintf("FieldType(%d)", int(t))
	}
}

// PgType returns the base Postgres column type for t, per spec §3.2.
//
// This is the base type only. A string field's `max:` upgrades "text" to
// "varchar(n)", and an enum field additionally emits a CHECK constraint;
// both are field-level and entity-level modifiers resolved by the code that
// builds the IR and the DDL emitter, not by FieldType, which has no access
// to a field's Max or an entity's enum values.
func (t FieldType) PgType() string {
	switch t {
	case FieldTypeUUID:
		return "uuid"
	case FieldTypeString:
		return "text"
	case FieldTypeText:
		return "text"
	case FieldTypeInt:
		return "integer"
	case FieldTypeBigint:
		return "bigint"
	case FieldTypeFloat:
		return "double precision"
	case FieldTypeDecimal:
		return "numeric"
	case FieldTypeBool:
		return "boolean"
	case FieldTypeTimestamp:
		return "timestamptz"
	case FieldTypeDate:
		return "date"
	case FieldTypeJSON:
		return "jsonb"
	case FieldTypeEnum:
		return "text"
	default:
		return fmt.Sprintf("<unknown FieldType %d>", int(t))
	}
}

// GoType returns the Go type for t, per spec §3.2. Nullable fields use the
// pointer form ("*string" rather than "string") so that "absent from the
// request" and "explicitly null" stay distinguishable at every layer that
// touches the value — see spec §6.5 and §6.6.
//
// FieldTypeEnum is not handled here. Revision 1 had this method return
// "string" for an enum's base type, which made GoType(true) answer
// "*string" for a nullable enum while the Field that owns it needs
// "*ArticleStatus" — a bare FieldType has no entity or field context to
// produce that name from, so "string" was a plausible-looking wrong answer,
// not a simplification. Rather than guess, GoType refuses to answer for
// FieldTypeEnum and returns an explicitly unusable placeholder instead;
// Field.GoType is what actually resolves an enum's Go type, using the
// field's EnumGoType.
func (t FieldType) GoType(nullable bool) string {
	if t == FieldTypeEnum {
		return "<invalid: FieldType.GoType cannot answer for an enum, use Field.GoType>"
	}
	base := t.goTypeBase()
	if nullable {
		return "*" + base
	}
	return base
}

func (t FieldType) goTypeBase() string {
	switch t {
	case FieldTypeUUID:
		return "pgtype.UUID"
	case FieldTypeString:
		return "string"
	case FieldTypeText:
		return "string"
	case FieldTypeInt:
		return "int32"
	case FieldTypeBigint:
		return "int64"
	case FieldTypeFloat:
		return "float64"
	case FieldTypeDecimal:
		return "pgtype.Numeric"
	case FieldTypeBool:
		return "bool"
	case FieldTypeTimestamp:
		return "time.Time"
	case FieldTypeDate:
		return "time.Time"
	case FieldTypeJSON:
		return "json.RawMessage"
	default:
		// FieldTypeEnum never reaches here: GoType intercepts it above and
		// returns before calling this helper. Any other out-of-range value
		// falls through to this same placeholder.
		return fmt.Sprintf("<unknown FieldType %d>", int(t))
	}
}
