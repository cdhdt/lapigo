package ir

import (
	"fmt"

	"github.com/cdhdt/lapigo/internal/source"
)

// Field is one column of an Entity, resolved from a YAML field declaration
// (spec §3.1).
//
// GoType and PgType are computed methods below, never stored fields (spec
// §2.2). Storing them alongside the FieldType they derive from let a
// resolver construct Field{Type: FieldTypeInt, GoType: "string", PgType:
// "double precision"} with nothing objecting — a Go string bound to a
// double precision column. The only genuine inputs to the resolved type are
// Type, Nullable, Max (for varchar(n)) and EnumGoType (which needs the
// entity name, so it cannot live on FieldType alone); deriving everything
// else makes a stored/derived mismatch unrepresentable rather than merely
// discouraged.
type Field struct {
	Name      source.At[string]
	GoName    string
	Column    string
	Type      FieldType
	Nullable  bool
	Unique    bool
	PK        bool
	ReadOnly  bool // never accepted from a request
	Immutable bool // accepted on create, rejected on update
	Version   bool // optimistic concurrency column; at most one per entity, enforced by Schema.Freeze
	Max       *int // string length constraint; upgrades PgType from "text" to "varchar(n)"

	// EnumGoType is the generated Go type name for an enum field (e.g.
	// "ArticleStatus"). Set only when Type == FieldTypeEnum. FieldType alone
	// — a bare enum with no entity or field context — cannot produce this
	// name, which is why GoType special-cases FieldTypeEnum instead of
	// delegating to FieldType.GoType for it.
	EnumGoType string
	EnumValues []EnumValue
	Default    *DefaultValue
}

// EnumValue is one member of an enum field's `values:` list (spec §3.1),
// paired with the exported Go identifier the generator will emit for it
// (e.g. "InProgress" for "in_progress").
//
// GoName exists on the element, not computed on demand from Name.Value,
// for the same reason Field.GoName and Entity.GoName are stored fields
// rather than methods: it is what the collision checks in
// internal/validate/names.go compare against, and computing it twice --
// once here, once in the validator -- is exactly the kind of duplicated
// policy CLAUDE.md's "templates never see raw YAML" boundary exists to rule
// out. internal/parse computes it with the same goName function used for
// every other identifier in the schema (see internal/parse/names.go's
// goName), not a second casing function of its own.
type EnumValue struct {
	Name   source.At[string]
	GoName string
}

// GoType returns f's Go type, per spec §3.2. It defers to FieldType.GoType
// for every scalar except enum: an enum's generated type name is specific to
// this field (EnumGoType), and FieldType.GoType deliberately refuses to
// guess it — see that method's doc comment for the contradiction storing
// "string" there caused.
func (f *Field) GoType() string {
	if f.Type == FieldTypeEnum {
		if f.Nullable {
			return "*" + f.EnumGoType
		}
		return f.EnumGoType
	}
	return f.Type.GoType(f.Nullable)
}

// PgType returns f's Postgres column type, per spec §3.2. The one modifier
// FieldType cannot express by itself is a string field's Max, which upgrades
// the base "text" to "varchar(n)" (spec §3.1); every other type is exactly
// FieldType's base mapping, including enum, whose "text" base needs no
// per-field context.
func (f *Field) PgType() string {
	if f.Type == FieldTypeString && f.Max != nil {
		return fmt.Sprintf("varchar(%d)", *f.Max)
	}
	return f.Type.PgType()
}

// IsSortEligible reports why f may not appear as a sort key, per spec §3.3
// rules 3 and 4, or returns the empty string if it may. It does not check
// uniqueness: that constraint applies to the last key of a SortSpec, not to
// every field the spec touches, and is therefore the validator's concern,
// not a single field's.
//
// One return value, not (bool, string): the bool was redundant, since every
// caller already branches on whether the reason is empty.
func (f *Field) IsSortEligible() string {
	if f.Nullable {
		return "field is nullable: a keyset comparison against NULL yields unknown, silently dropping every row after the seek"
	}
	switch f.Type {
	case FieldTypeUUID, FieldTypeString, FieldTypeText, FieldTypeInt, FieldTypeBigint,
		FieldTypeFloat, FieldTypeBool, FieldTypeTimestamp, FieldTypeDate, FieldTypeEnum:
		return ""
	case FieldTypeDecimal:
		return "field type is decimal: pgtype.Numeric marshals to a bare JSON number, which a generic decoder routes through float64, defeating the exactness the type exists for"
	case FieldTypeJSON:
		return "field type is json: not ordered, so it cannot serve as a keyset comparison"
	default:
		// Every known FieldType is listed explicitly above, on both sides of
		// the eligible/ineligible line. An out-of-range value (a bug in
		// whoever constructed the Field, since the validator should never
		// let one through) falls through to here rather than silently
		// passing as eligible — FieldType(99) with no default branch used
		// to do exactly that.
		return fmt.Sprintf("field type %s is not a known FieldType, so its sort eligibility cannot be determined", f.Type)
	}
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

// String returns the YAML `default:` keyword k was parsed from, matching the
// pattern of FieldType.String, FilterOp.String and EndpointKind.String: an
// out-of-range value gets an explicit, self-naming placeholder rather than a
// value that looks like a real keyword.
func (k DefaultKind) String() string {
	switch k {
	case DefaultNow:
		return "now"
	case DefaultUUID:
		return "uuid"
	case DefaultLiteral:
		return "literal"
	default:
		return fmt.Sprintf("DefaultKind(%d)", int(k))
	}
}

// DefaultValue is a field's `default:` clause. A field carrying one is
// excluded from create input (spec §6.5): the caller cannot set a value the
// database or the generator will supply.
type DefaultValue struct {
	Kind    DefaultKind
	Literal string // populated only when Kind == DefaultLiteral
}
