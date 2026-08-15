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
// For FieldTypeEnum, GoType reports only the underlying representation
// ("string"): the actual generated type name (e.g. "ArticleStatus") is
// specific to one field of one entity, which FieldType — a bare enum with no
// such context — cannot produce. That resolved name lives on Field.GoType
// instead, computed by whatever builds the IR.
func (t FieldType) GoType(nullable bool) string {
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
	case FieldTypeEnum:
		return "string"
	default:
		return fmt.Sprintf("<unknown FieldType %d>", int(t))
	}
}
