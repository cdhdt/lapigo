package parse

import (
	"sort"

	"github.com/goccy/go-yaml/ast"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// fieldKeys is the complete set of options a field's mapping may declare
// (spec §3.1's table), used both to reject unknown keys and to suggest the
// closest known one for a typo.
var fieldKeys = []string{
	"type", "pk", "required", "unique", "max", "values",
	"target", "on_delete", "default", "readonly", "immutable", "version",
}

// fieldTypeKeywords maps every YAML `type:` keyword the schema format
// accepts (spec §3.2) to its resolved ir.FieldType. "belongsTo" is
// deliberately absent: it does not name an ir.FieldType at all, and is
// dispatched to relation building instead of field building (see
// buildFieldsAndRelations).
var fieldTypeKeywords = map[string]ir.FieldType{
	"uuid":      ir.FieldTypeUUID,
	"string":    ir.FieldTypeString,
	"text":      ir.FieldTypeText,
	"int":       ir.FieldTypeInt,
	"bigint":    ir.FieldTypeBigint,
	"float":     ir.FieldTypeFloat,
	"decimal":   ir.FieldTypeDecimal,
	"bool":      ir.FieldTypeBool,
	"timestamp": ir.FieldTypeTimestamp,
	"date":      ir.FieldTypeDate,
	"json":      ir.FieldTypeJSON,
	"enum":      ir.FieldTypeEnum,
}

// typeKeywordSuggestions is the vocabulary checkUnknownKeys-style edit
// distance suggestion draws from for an unrecognised `type:` value --
// every ir.FieldType keyword plus "belongsTo", since a typo of either is
// equally plausible ("blongsTo", "sting").
//
// Sorted, not left in map iteration order -- see endpointKeywordNames' doc
// comment for why an unsorted, map-derived vocabulary makes suggest's
// output depend on process-randomised map order.
var typeKeywordSuggestions = func() []string {
	out := make([]string, 0, len(fieldTypeKeywords)+1)
	for k := range fieldTypeKeywords {
		out = append(out, k)
	}
	out = append(out, "belongsTo")
	sort.Strings(out)
	return out
}()

// onDeleteKeywords maps every YAML `on_delete:` keyword (spec §3.1) to the
// SQL keyword ir.Relation.OnDelete stores.
var onDeleteKeywords = map[string]string{
	"restrict": "RESTRICT",
	"cascade":  "CASCADE",
	"set_null": "SET NULL",
}

// buildField resolves one `fields:` entry -- name and its option mapping --
// into an *ir.Field. entityGoName is needed only to compute EnumGoType
// (spec: "e.g. ArticleStatus" -- specific to this field in this entity, so
// FieldType itself cannot produce it; see ir.Field.GoType's doc comment).
//
// belongsTo is handled by the caller, not here: a `type: belongsTo` entry
// produces an ir.Relation, not (only) an ir.Field, and needs the two-pass
// target resolution buildField has no part in.
func (r *resolver) buildField(name source.At[string], entityGoName string, body ast.Node) *ir.Field {
	r.requireExportableName(name, "field name")

	m, ok := r.requireMapping(body, fieldContext(name.Value))
	if !ok {
		return nil
	}
	entries := r.entries(m)
	r.checkUnknownKeys(entries, fieldContext(name.Value), fieldKeys)

	f := &ir.Field{
		Name:   name,
		GoName: goName(name.Value),
		Column: name.Value,
	}

	var required, pk bool
	var typeSeen bool
	var maxSeen, valuesSeen bool
	var maxAt, valuesAt source.At[string]

	for _, e := range entries {
		switch e.Key.Value {
		case "type":
			// typeSeen is set regardless of whether the value type-checks:
			// a `type: 123` has a `type:` key, just a bad value, and should
			// report only the wrong-type diagnostic below -- not also "has
			// no type", which would be actively misleading about what the
			// user needs to fix.
			typeSeen = true
			s, ok := r.requireString(e.Value, fieldContext(name.Value)+" `type`")
			if !ok {
				continue
			}
			ft, ok := fieldTypeKeywords[s.Value]
			if !ok {
				hint := ""
				if sug := suggest(s.Value, typeKeywordSuggestions); sug != "" {
					hint = "did you mean `" + sug + "`?"
				}
				r.addAt(atOf(s.Value, s.GetToken()), hint, "unknown type %q", s.Value)
				continue
			}
			f.Type = ft
		case "pk":
			b, ok := r.requireBool(e.Value, fieldContext(name.Value)+" `pk`")
			if ok {
				pk = b.Value
				f.PK = b.Value
			}
		case "required":
			b, ok := r.requireBool(e.Value, fieldContext(name.Value)+" `required`")
			if ok {
				required = b.Value
			}
		case "unique":
			b, ok := r.requireBool(e.Value, fieldContext(name.Value)+" `unique`")
			if ok {
				f.Unique = b.Value
			}
		case "readonly":
			b, ok := r.requireBool(e.Value, fieldContext(name.Value)+" `readonly`")
			if ok {
				f.ReadOnly = b.Value
			}
		case "immutable":
			b, ok := r.requireBool(e.Value, fieldContext(name.Value)+" `immutable`")
			if ok {
				f.Immutable = b.Value
			}
		case "version":
			b, ok := r.requireBool(e.Value, fieldContext(name.Value)+" `version`")
			if ok {
				f.Version = b.Value
			}
		case "max":
			// Whether `max:` is even meaningful for this field's type can
			// only be decided once the whole mapping has been walked --
			// `type:` is not guaranteed to appear before `max:` in the
			// written YAML -- so that check, and the values: one below it,
			// happen once after this loop, not here. The range check
			// (max must be positive) does not depend on type and is
			// checked immediately, at the token that carries the position.
			maxSeen = true
			maxAt = e.Key
			i, ok := r.requireInt(e.Value, fieldContext(name.Value)+" `max`")
			if ok {
				v, ok := intNodeValue(i)
				if !ok {
					r.addAt(atOf(i.Token.Value, i.GetToken()), "", "%s `max` is out of range", fieldContext(name.Value))
					continue
				}
				if v <= 0 {
					r.addAt(atOf(i.Token.Value, i.GetToken()), "", "%s `max` must be a positive integer, found %d", fieldContext(name.Value), v)
					continue
				}
				f.Max = &v
			}
		case "values":
			valuesSeen = true
			valuesAt = e.Key
			f.EnumValues = r.buildEnumValues(e.Value, name.Value)
		case "default":
			f.Default = r.buildDefault(e.Value, name.Value)
		case "target", "on_delete":
			// Consumed by buildRelation for a belongsTo entry; buildField
			// is never called for one (see buildFieldsAndRelations), so
			// reaching here means `target`/`on_delete` was set on a
			// non-belongsTo field.
			r.addAt(e.Key, "`target` and `on_delete` only apply to `type: belongsTo`",
				"key %q is not valid on a field of type %q", e.Key.Value, fieldTypeName(f.Type))
		}
	}

	if !typeSeen {
		r.addAt(name, "add a `type:` key, e.g. `type: string`", "field %q has no `type`", name.Value)
	}

	// `max:` and `values:` are meaningful for exactly one type each (spec
	// §3.1); on any other type they are accepted text that means nothing --
	// `max: -1` on an int field, `values:` on a string field setting
	// EnumGoType with nothing that will ever read it. Checked here, once
	// f.Type is fully known regardless of where `type:` fell in the
	// mapping, rather than inline in the switch above.
	if maxSeen && f.Type != ir.FieldTypeString {
		r.addAt(maxAt, "`max` only applies to `type: string`",
			"key %q is not valid on a field of type %q", maxAt.Value, fieldTypeName(f.Type))
		f.Max = nil
	}
	if valuesSeen && f.Type != ir.FieldTypeEnum {
		r.addAt(valuesAt, "`values` only applies to `type: enum`",
			"key %q is not valid on a field of type %q", valuesAt.Value, fieldTypeName(f.Type))
		f.EnumValues = nil
	}
	if f.Type == ir.FieldTypeEnum {
		if len(f.EnumValues) == 0 {
			// EnumGoType is deliberately left unset ("") in this branch,
			// not just EnumValues empty: a diagnostic here means the schema
			// is discarded (see Parse), but leaving EnumGoType at its zero
			// value keeps this field from ever looking like a validly
			// resolved enum to anything that inspects it before that
			// discard happens.
			r.addAt(name, "add a `values:` list with at least one member, e.g. `values: [draft, published]`",
				"field `%s` has type `enum` but no `values`", name.Value)
		} else {
			f.EnumGoType = entityGoName + goName(name.Value)
		}
	}

	f.Nullable = !required && !pk

	return f
}

// fieldTypeName renders t as the YAML keyword a diagnostic should name --
// ir.FieldType.String already does exactly this, so fieldTypeName exists
// only to keep call sites in this file from reaching past buildField's own
// vocabulary into ir's.
func fieldTypeName(t ir.FieldType) string { return t.String() }

// buildEnumValues resolves a `values:` sequence into the At[string] slice
// ir.Field.EnumValues holds, reporting a diagnostic for any element that is
// not a plain string.
func (r *resolver) buildEnumValues(n ast.Node, fieldName string) []source.At[string] {
	seq, ok := r.requireSequence(n, fieldContext(fieldName)+" `values`")
	if !ok {
		return nil
	}
	out := make([]source.At[string], 0, len(seq.Values))
	for _, v := range seq.Values {
		s, ok := r.requireString(v, fieldContext(fieldName)+" `values` entry")
		if !ok {
			continue
		}
		out = append(out, atOf(s.Value, s.GetToken()))
	}
	return out
}

// buildDefault resolves a `default:` value into an *ir.DefaultValue: the
// two reserved keywords (spec §3.2) or a literal, carried as the scalar's
// raw textual form.
func (r *resolver) buildDefault(n ast.Node, fieldName string) *ir.DefaultValue {
	s, ok := n.(*ast.StringNode)
	if ok {
		switch s.Value {
		case "now":
			return &ir.DefaultValue{Kind: ir.DefaultNow}
		case "uuid":
			return &ir.DefaultValue{Kind: ir.DefaultUUID}
		default:
			return &ir.DefaultValue{Kind: ir.DefaultLiteral, Literal: s.Value}
		}
	}
	if _, ok := n.(ast.ScalarNode); !ok {
		r.addNode(n, "", "%s `default` must be a scalar, found %s", fieldContext(fieldName), nodeTypeName(n))
		return nil
	}
	return &ir.DefaultValue{Kind: ir.DefaultLiteral, Literal: n.GetToken().Value}
}

func fieldContext(name string) string {
	return "field `" + name + "`"
}
