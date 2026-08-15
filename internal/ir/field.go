package ir

// Field is one column of an Entity, resolved from a YAML field declaration
// (spec §3.1).
//
// GoType and PgType are plain, already-resolved strings rather than
// FieldType.GoType()/PgType() calls: for most fields they agree with the
// FieldType default, but a string field's Max upgrades PgType from "text" to
// "varchar(n)", and an enum field's GoType is the entity-specific generated
// type name (e.g. "ArticleStatus"), neither of which FieldType alone — a
// bare enum with no entity or field context — can produce. Whatever builds
// the IR resolves the final strings once so no downstream package
// (template, DDL emitter) has to repeat that resolution.
type Field struct {
	Name       At[string]
	GoName     string
	Column     string
	Type       FieldType
	GoType     string
	PgType     string
	Nullable   bool
	Unique     bool
	PK         bool
	ReadOnly   bool // never accepted from a request
	Immutable  bool // accepted on create, rejected on update
	Version    bool // optimistic concurrency column
	Max        *int // string length constraint
	EnumValues []At[string]
	Default    *DefaultValue
}

// IsSortEligible reports whether f may appear as a sort key, per spec §3.3
// rules 3 and 4. It does not check uniqueness: that constraint applies only
// to the last key of a SortSpec, not to every field the spec touches, and is
// therefore the validator's concern, not a single field's.
//
// A field failing this check returns a human-readable reason suitable for a
// diagnostic; a field passing it returns an empty reason.
func (f *Field) IsSortEligible() (bool, string) {
	if f.Nullable {
		return false, "field is nullable: a keyset comparison against NULL yields unknown, silently dropping every row after the seek"
	}
	switch f.Type {
	case FieldTypeDecimal:
		return false, "field type is decimal: pgtype.Numeric marshals to a bare JSON number, which a generic decoder routes through float64, defeating the exactness the type exists for"
	case FieldTypeJSON:
		return false, "field type is json: not ordered, so it cannot serve as a keyset comparison"
	}
	return true, ""
}

// DefaultKind identifies which of the three `default:` forms (spec §3.1) a
// field carries.
type DefaultKind int

const (
	// DefaultNow is `default: now` — CURRENT_TIMESTAMP at insert.
	DefaultNow DefaultKind = iota
	// DefaultUUID is `default: uuid` — generated client-side with
	// crypto/rand, not by a database default (spec §3.2), so create returns
	// the identifier without a round trip.
	DefaultUUID
	// DefaultLiteral is `default: <value>` — the literal is carried in
	// DefaultValue.Literal.
	DefaultLiteral
)

// DefaultValue is a field's `default:` clause. A field carrying one is
// excluded from create input (spec §6.5): the caller cannot set a value the
// database or the generator will supply.
type DefaultValue struct {
	Kind    DefaultKind
	Literal string // populated only when Kind == DefaultLiteral
}
