package gen

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/cdhdt/lapigo/internal/ir"
)

// This file is step 7's cursor codec (spec §7.4, §7.5, §7.6): the store
// package's fixed cursor envelope and, per entity that lists, the typed
// encoder and decoder for that entity's sort keys.
//
// Everything schema-derived is decided here, in Go, and the template
// interpolates it (spec §5.1). That is not ceremony for this file in
// particular: the fingerprint is a hash over a canonical form whose byte
// order is the whole point (spec §7.4 -- "iterating a Go map to build it
// would make a client reject its own cursor nondeterministically"), and a
// canonical form assembled from template text is one that no unit test can
// address.

// cursorShapeDomain is the first line of every canonical form, and it is
// what keeps the digest addressed to this construction: a fingerprint is a
// hash of a text, and a text with no domain separator can collide with any
// other text this project ever hashes. It carries the format version
// because a change to the cursor's shape must invalidate the cursors
// already in flight, which is exactly what a changed fingerprint does.
const cursorShapeDomain = "lapigo-cursor-fingerprint-v1"

// cursorFormatVersion is the value the generated cursor's `v` member
// carries, and the only value the generated decoder accepts (spec §7.4).
const cursorFormatVersion = 1

// cursorFingerprintBytes is how much of the SHA-256 digest the generated
// constant carries: 16 bytes, 32 hex characters.
//
// Truncation is safe here and the reason is spec §7.4's own: the
// fingerprint is a SHAPE check, not an authentication tag. Phase 1 ships no
// HMAC deliberately -- "a forged cursor grants no unauthorised access,
// since authorisation filters are applied server-side from the request and
// never from the cursor" -- so what this value has to resist is an
// accidental collision between two shapes in one schema, not an adversary
// searching for one. 64 bits of digest against a schema holding a handful
// of entities is not the weak link in that chain, and a cursor is a URL
// parameter that lands in access logs and Referer headers, where 32
// characters of hex are cheaper than 64.
const cursorFingerprintBytes = 16

// canonicalCursorShape renders the query shape e pages under, in the
// canonical form spec §7.4 requires the fingerprint to be computed over:
// the entity name, the sort spec and the filter set, "filters sorted by
// name, values in declaration order".
//
//	lapigo-cursor-fingerprint-v1
//	entity=article
//	sort=-created_at,-id
//	filters=author_id:eq,status:eq
//
// Four properties of this text are load-bearing:
//
//   - The entity name is in it. Revision 1's fingerprint omitted it, so two
//     entities sharing a sort and filter shape produced interchangeable
//     cursors and one entity's cursor replayed against another (spec §7.4,
//     §12).
//   - The filters come from ir.Entity.SortedFilters, never from e.Filters.
//     Declaration order is the DDL emitter's (internal/ddl derives one
//     index per declared filter in that order, spec §7.2), so the canonical
//     by-name view is a separate accessor rather than a sort in place --
//     and it is the by-name view that spec §7.4 names.
//   - Nothing here iterates a map. A map's iteration order is randomised
//     per process (spec §5.3), so a fingerprint built from one would differ
//     between two runs of the same generator and, worse, between two
//     instances of the same generated binary.
//   - Every value it interpolates comes from the parser's identifier
//     grammar (^[A-Za-z_][A-Za-z0-9_]*$, internal/parse/names.go), which
//     admits neither the separators used here (newline, "=", ",", ":") nor
//     anything that could forge one. The form is therefore unambiguous
//     without escaping, and stays so for as long as that grammar does.
//
// Two things it deliberately leaves out.
//
// The sort keys' TYPES: a key whose type changed under a regenerated binary
// is already caught, one layer down, by the strict typed decoding spec §7.4
// requires -- an old value that no longer fits the new type is a decode
// error, and one that does fit is a position that is still valid in the
// same sort order. Hashing the types would turn one 400 into another 400
// and buy nothing.
//
// The filter VALUES a page was actually queried with: a cursor is a
// position in a sort order, and that position stays a valid position when
// the caller changes ?status= between pages -- they simply resume mid-way
// through the new result set, which is what a keyset cursor means. Putting
// the values in would also make the fingerprint a per-request computation
// rather than the constant it is, for a case that is not an error.
//
// Columns, not field names, are what it interpolates: Field.Column is the
// vocabulary every name on the wire uses (spec §6.9.2), and the cursor's
// own member keys are columns, so a fingerprint built from columns covers
// the member names the decoder will look for. The ORDER of the filter
// entries is still SortedFilters' -- by field name -- because that is the
// order spec §7.4 names; the two agree on every schema anyway, since a
// field and its column are one-to-one within an entity.
func canonicalCursorShape(e *ir.Entity) string {
	var b strings.Builder
	b.WriteString(cursorShapeDomain)
	b.WriteString("\nentity=")
	b.WriteString(e.Name)

	b.WriteString("\nsort=")
	for i, k := range e.Sort.Keys {
		if i != 0 {
			b.WriteString(",")
		}
		if e.Sort.Desc {
			b.WriteString("-")
		} else {
			b.WriteString("+")
		}
		b.WriteString(k.Field.Column)
	}

	b.WriteString("\nfilters=")
	for i, f := range e.SortedFilters() {
		if i != 0 {
			b.WriteString(",")
		}
		b.WriteString(f.Field.Column)
		b.WriteString(":")
		b.WriteString(f.Op.String())
	}

	b.WriteString("\n")
	return b.String()
}

// cursorFingerprint returns the constant the generated code carries for e:
// the truncated SHA-256 of canonicalCursorShape(e), hex-encoded.
//
// It is a generation-time constant, not a runtime computation, because
// every one of its inputs is fixed at generation time. A cursor minted
// against another entity, or against this entity before its sort or its
// filters changed, carries a different constant and is rejected by the
// generated decoder (spec §7.4).
func cursorFingerprint(e *ir.Entity) string {
	sum := sha256.Sum256([]byte(canonicalCursorShape(e)))
	return hex.EncodeToString(sum[:cursorFingerprintBytes])
}

// cursorFileData is what templates/store_cursor.tmpl is executed against.
// It is not fileData: the cursor codec is one file for the whole schema --
// its fixed half is schema-independent and its typed half repeats per
// listing entity -- so the file is about a set of entities rather than
// about one, which is the field fileData carries.
type cursorFileData struct {
	Package string
	Imports []string
	// FormatVersion is spec §7.4's format version, rendered into the
	// generated constant. It lives here rather than as a literal in the
	// template so that the version the generator hashes into every
	// fingerprint (cursorShapeDomain) and the version it emits cannot drift
	// apart unnoticed.
	FormatVersion int
	// Entities are the schema's listing entities, in schema order (sorted
	// by name, ir.Schema.Freeze), never a map (spec §5.3).
	Entities []cursorEntity
}

// cursorEntity is one listing entity's share of the cursor file: the
// identifiers its codec declares, the fingerprint constant it pins, and its
// sort keys already resolved to Go text.
type cursorEntity struct {
	// Name is the entity as written, for comments only.
	Name string
	// GoName is the entity's Go name ("Article"), used for the model type
	// and for the exported halves of the generated identifiers.
	GoName string
	// VarPrefix is GoName with its first rune lowercased ("article"): the
	// stem of every unexported identifier this entity contributes.
	VarPrefix string
	// Fingerprint is the constant's value (cursorFingerprint).
	Fingerprint string
	// ShapeLines is canonicalCursorShape split into lines, so the generated
	// doc comment can show the exact text that was hashed. A reader who
	// needs to know why a cursor was rejected can then compare shapes
	// instead of digests.
	ShapeLines []string
	// Keys are the entity's sort keys, in sort order -- which is also the
	// order the keyset predicate binds them in.
	Keys []cursorKey
	// HasTimeKey reports whether any of Keys is a timestamp or a date. It
	// gates spec §7.5's paragraph in the generated encoder's doc comment:
	// an entity keyed on a uuid alone is not subject to those rules, and a
	// generated file that explains a rule it does not exercise teaches its
	// reader that the comments are boilerplate.
	HasTimeKey bool
	// HasValidKey reports whether any of Keys carries a pgtype validity
	// flag, and gates the sentence about it in the generated decoder's doc
	// comment, for the same reason as HasTimeKey.
	HasValidKey bool
}

// cursorKey is one sort key as the template needs it.
type cursorKey struct {
	// GoName is the model struct's field name ("CreatedAt").
	GoName string
	// Column is the member's key on the wire (spec §6.9.2, §7.4).
	Column string
	// Tag is the rendered struct tag naming Column.
	Tag string
	// GoType is the key's Go type as written INSIDE the store package:
	// package-qualified for a model-owned enum type, which is the one
	// sort-key type whose declaration lives in another package.
	GoType string
	// Local is the local variable the encoder takes the address of. It is
	// "key" + GoName rather than a lowercased GoName so that a field named
	// "row" cannot shadow the encoder's own parameter.
	Local string
	// EncodeExpr is what that local is assigned from, e.g. "row.CreatedAt.UTC()".
	EncodeExpr string
	// Normalise is the expression the decoder normalises the decoded value
	// with, or the empty string when the value needs none. Only a
	// time.Time does: a forged cursor may carry any offset, and binding a
	// UTC value keeps what reaches the query identical to what encoding
	// produced (spec §7.5 rule 2).
	Normalise string
	// CheckValid marks a pgtype-backed key whose decoded value carries a
	// Valid flag. pgtype.UUID unmarshals the JSON literal null into
	// UUID{Valid: false} without an error of its own, and binding that
	// would seek against NULL -- which compares unknown and silently
	// returns an empty page (spec §3.3 rule 3's reasoning, applied to the
	// cursor rather than the column).
	//
	// The check it emits is unreachable through today's encoding/json,
	// which leaves a pointer member nil for a null rather than calling the
	// pointee's Unmarshaler, so the nil check rejects it first. Both halves
	// of that -- pgtype's acceptance of null and encoding/json's pointer
	// rule -- are pinned by a test in the generated module
	// (TestPgtypeUUID_NullIsUnreachableThroughAPointer, cursor_test.go's
	// round-trip driver), because the guard is only worth its line if the
	// day one of them changes is a day this fails.
	CheckValid bool
}

// planStoreCursorFiles builds the store package's cursor file, or nothing
// at all when no entity in s declares `list`.
//
// Nothing at all, rather than the fixed half on its own the way
// model/optional.go is always emitted: optional.go declares a generic TYPE,
// which costs an unused project nothing, while this file's fixed half is
// four unexported FUNCTIONS. Emitting them into a project that never
// paginates leaves dead code in a tree the user is meant to read -- and
// dead unexported functions are what a user's own linter reports. The
// condition is stated once, here, so there is no second copy to drift.
func planStoreCursorFiles(s *ir.Schema, modulePath string) ([]OutputFile, error) {
	entities := listingEntities(s)
	if len(entities) == 0 {
		return nil, nil
	}

	data := cursorFileData{
		Package:       packageStore,
		FormatVersion: cursorFormatVersion,
	}
	for _, e := range entities {
		ce, err := cursorEntityOf(e)
		if err != nil {
			return nil, err
		}
		data.Entities = append(data.Entities, ce)
	}

	imports, err := storeCursorImports(entities, modulePath)
	if err != nil {
		return nil, err
	}
	data.Imports = imports

	return []OutputFile{{
		Path:     genRoot + "/" + packageStore + "/cursor.go",
		Package:  packageStore,
		Imports:  imports,
		Template: "store/cursor.go",
		Data:     data,
	}}, nil
}

// listingEntities returns the entities of s that generate a list endpoint,
// in schema order. They are the only ones that mint a cursor: spec §6.9.4's
// `after` is a list parameter and nothing else takes one.
func listingEntities(s *ir.Schema) []*ir.Entity {
	var out []*ir.Entity
	for _, e := range s.Entities {
		if e.HasList() {
			out = append(out, e)
		}
	}
	return out
}

// cursorEntityOf resolves e into the template's view of it.
func cursorEntityOf(e *ir.Entity) (cursorEntity, error) {
	out := cursorEntity{
		Name:        e.Name,
		GoName:      e.GoName,
		VarPrefix:   lowerFirst(e.GoName),
		Fingerprint: cursorFingerprint(e),
		ShapeLines:  strings.Split(strings.TrimSuffix(canonicalCursorShape(e), "\n"), "\n"),
	}

	for _, k := range e.Sort.Keys {
		f := k.Field
		tag, err := jsonTag(f.Column)
		if err != nil {
			return cursorEntity{}, fmt.Errorf("gen: entity %q, sort key %q: %w", e.Name, f.Name.Value, err)
		}

		key := cursorKey{
			GoName:     f.GoName,
			Column:     f.Column,
			Tag:        tag,
			GoType:     cursorKeyGoType(f),
			Local:      "key" + f.GoName,
			EncodeExpr: "row." + f.GoName,
		}
		switch f.Type {
		case ir.FieldTypeTimestamp, ir.FieldTypeDate:
			// Spec §7.5 rule 2: normalise to UTC before serialising, so
			// that two instances whose hosts sit in different time zones
			// mint the same bytes for the same instant.
			key.EncodeExpr += ".UTC()"
			key.Normalise = ".UTC()"
			out.HasTimeKey = true
		case ir.FieldTypeUUID:
			key.CheckValid = true
			out.HasValidKey = true
		}
		out.Keys = append(out.Keys, key)
	}
	return out, nil
}

// cursorKeyGoType returns f's Go type as the store package must spell it.
//
// It is ValueGoType, not GoType, and the difference cannot arise: a sort
// key is never nullable (spec §3.3 rule 3, enforced by internal/validate),
// so the pointer form GoType would add for a nullable field is unreachable
// here. ValueGoType is still what this asks for, because a pointer in a
// cursor's key struct already means something else -- "the member was sent"
// -- and layering nullability onto that would give absent two spellings.
//
// The one type that needs qualifying is an enum: its generated type is
// declared in the model package, and this file is in store.
func cursorKeyGoType(f *ir.Field) string {
	if f.Type == ir.FieldTypeEnum {
		return packageModel + "." + f.EnumGoType
	}
	return f.ValueGoType()
}

// storeCursorImports returns the sorted, deduplicated import set of the
// cursor file.
//
// The five standard-library packages are unconditional: they are what the
// fixed half of the file uses, and the fixed half is emitted whenever the
// file is. model is unconditional too, because the file is only planned
// when at least one entity lists, and every encoder takes that entity's
// model struct. Everything else follows the sort keys' own types, through
// importsForFieldType -- the same function the model and hooks files use,
// so a new field type gets its import decided in exactly one place.
func storeCursorImports(entities []*ir.Entity, modulePath string) ([]string, error) {
	seen := map[string]bool{
		"bytes":                     true,
		"encoding/base64":           true,
		"encoding/json":             true,
		"errors":                    true,
		"fmt":                       true,
		modelImportPath(modulePath): true,
	}

	for _, e := range entities {
		for _, k := range e.Sort.Keys {
			paths, err := importsForFieldType(k.Field.Type)
			if err != nil {
				return nil, fmt.Errorf("gen: entity %q cursor: sort key %q: %w", e.Name, k.Field.Name.Value, err)
			}
			for _, p := range paths {
				seen[p] = true
			}
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	// Sorted before it leaves the function: derived from a map, whose
	// iteration order Go randomises per process (spec §5.3).
	sort.Strings(out)
	return out, nil
}

// lowerFirst returns s with its first rune lowercased -- the transform from
// a generated exported name to the unexported stem this file's identifiers
// are built on ("Article" to "article").
//
// It is injective over the names it is given, which is what keeps two
// entities from colliding on one identifier: every Entity.GoName is
// export-cased by internal/parse, so two distinct GoNames cannot differ in
// the first rune's case alone. Runes, not bytes: an entity named "Éclair"
// has a multi-byte first rune, and slicing one byte off it produces
// invalid UTF-8 rather than an identifier.
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToLower(r[0])
	return string(r)
}
