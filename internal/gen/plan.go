package gen

import (
	"fmt"
	"sort"

	"github.com/cdhdt/lapigo/internal/ir"
)

// genRoot is the module-root-relative directory every generated Go file
// lives under (spec §6.1). Slash-separated, never filepath-joined: these are
// map keys and lock entries, not paths on the generating machine, and they
// must be the same bytes on every operating system.
const genRoot = "internal/gen"

// The generated packages of spec §6.1's tree. model is the only one step 5
// renders; store, httpapi and hooks land with steps 6 to 8 and are named
// here so that the pending rows in names.go and the plan below cannot drift
// apart on a spelling.
const (
	packageModel   = "model"
	packageStore   = "store"
	packageHooks   = "hooks"
	packageHTTPAPI = "httpapi"
)

// pgtypeImport is the one non-standard-library import generated code is
// allowed (spec §2.3, CLAUDE.md decision 3). It is a constant so that a
// second spelling of it cannot appear anywhere in this package.
const pgtypeImport = "github.com/jackc/pgx/v5/pgtype"

// pgxImport is pgx's own root package, needed wherever generated code names
// pgx.Tx -- every hook signature carries one (spec §6.3).
const pgxImport = "github.com/jackc/pgx/v5"

// OutputFile is one Go file Generate will produce, with everything needed to
// render it decided before any template runs (spec §2.1 step 6, §2.2).
//
// Imports is a property of the file, not of an entity: one entity spans four
// packages with different needs, so there is no single import set an
// ir.Entity could carry. It is computed here, from the IR, and it is
// authoritative -- the formatter runs with FormatOnly and will neither add
// to it nor take from it (spec §5.2). The correction pass for a wrong set is
// the Go compiler: a missing import is "undefined", a surplus one is
// "imported and not used", and spec §8's compile tier sees both.
type OutputFile struct {
	// Path is module-root-relative and slash-separated, e.g.
	// "internal/gen/model/article.go".
	Path string
	// Package is the Go package clause the file carries.
	Package string
	// Imports is the file's complete import set, sorted and free of
	// duplicates. Grouping into standard-library and module blocks happens
	// at render time, in importBlock.
	Imports []string
	// Template is the name of the defined template that renders the file.
	Template string
	// Data is what that template is executed against.
	Data any
}

// fileData is what every template receives. Package and Imports come
// straight from the OutputFile so that the shared header template can render
// without knowing which file it is in; Entity is the IR node the file is
// about, and is nil for a file that is not per-entity -- spec §5.6's fixed
// per-package sets, such as model's Optional[T] (§6.5), hooks' Error (§6.4)
// and, still pending, store's cursor codec.
type fileData struct {
	Package string
	Imports []string
	Entity  *ir.Entity
}

// plan computes the complete set of output files for s, in a deterministic
// order (spec §2.1 step 6).
//
// modulePath is the target project's own module path -- the first line of
// its go.mod, e.g. "myapp" (spec §6.1's `lapigo new myapp`) -- and is needed
// starting with step 6 because hooks is the first generated package to
// import another one: every hook signature names a model type (spec §6.3),
// and Go has no import syntax relative to the current module, only the
// module's declared path plus the subdirectory. Generate does not read it
// from anywhere -- it is not in lapigo.yaml, and reading the generating
// machine's own go.mod would answer for the wrong module entirely -- so it
// is a parameter, supplied by step 9's CLI from the target project's own
// go.mod, exactly as Write's Options.Version is supplied rather than read
// from a package global.
//
// The order comes from ir.Schema.Entities, which Freeze guarantees is sorted
// by name, and from the fixed order of the loop body -- never from a map
// (spec §5.3). Generate returns a map, so the order does not reach the
// output on its own, but the plan is also what a later step iterates to
// write files, and an unstable plan would produce unstable diagnostics.
func plan(s *ir.Schema, modulePath string) ([]OutputFile, error) {
	if s == nil {
		return nil, fmt.Errorf("gen: plan called on a nil schema")
	}

	files := []OutputFile{planModelOptionalFile()}
	for _, e := range s.Entities {
		f, err := planModelFile(e)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}

	hooksFiles, err := planHooksFiles(s, modulePath)
	if err != nil {
		return nil, err
	}
	files = append(files, hooksFiles...)

	// store last, after the packages it imports: the plan's order is the
	// order the generated tree reads in, and model comes before the hooks
	// that name its types and the store that fills them (spec §6.1).
	storeFiles, err := planStoreCursorFiles(s, modulePath)
	if err != nil {
		return nil, err
	}
	files = append(files, storeFiles...)

	return files, nil
}

// planModelOptionalFile builds the OutputFile for model's one fixed
// declaration, Optional[T] (spec §6.5, §5.6's model fixed row). It needs no
// Entity: nothing in it is schema-derived.
//
// Always emitted, the same way planHooksErrorFile always is: gating it on
// whether any entity in the schema has HasCreate() or HasUpdate() would be a
// second, drifting copy of the condition each per-entity CreateInput/
// UpdateInput already applies on its own, for no benefit -- an unused
// generic type costs a generated project nothing.
func planModelOptionalFile() OutputFile {
	imports := []string{"encoding/json"}
	return OutputFile{
		Path:     genRoot + "/" + packageModel + "/optional.go",
		Package:  packageModel,
		Imports:  imports,
		Template: "model/optional.go",
		Data: fileData{
			Package: packageModel,
			Imports: imports,
		},
	}
}

// planModelFile builds the OutputFile for e's model types: the struct that a
// scanned row fills and a handler marshals, one generated type and one
// constant per member for each of e's enum fields, and -- gated on
// HasCreate()/HasUpdate() -- the CreateInput/UpdateInput structs spec §6.5
// projects from the same field set (spec §5.6's model row).
//
// One file per entity, named after the entity as written. Entity.Name has
// passed the parser's identifier grammar, so it is safe as a path element,
// and Entities is sorted by name, so the file set is a deterministic
// function of the schema.
func planModelFile(e *ir.Entity) (OutputFile, error) {
	imports, err := modelImports(e)
	if err != nil {
		return OutputFile{}, err
	}
	return OutputFile{
		Path:     genRoot + "/" + packageModel + "/" + e.Name + ".go",
		Package:  packageModel,
		Imports:  imports,
		Template: "model/entity.go",
		Data: fileData{
			Package: packageModel,
			Imports: imports,
			Entity:  e,
		},
	}, nil
}

// modelImports returns the sorted, deduplicated import set of e's model
// file: whatever the Go types of e's fields need, and nothing else.
//
// This covers CreateInput/UpdateInput too, not only the model struct:
// ValueGoType strips a field's nullability but never its underlying
// package (a nullable uuid field and a non-nullable one both need pgtype,
// as *pgtype.UUID and pgtype.UUID respectively), so the import a field
// needs is a function of FieldType alone -- importsForFieldType already
// answers for both call sites without change.
//
// It walks Fields, not Relations. A belongsTo produces both an ir.Field --
// the foreign key scalar, whose Type is rewritten to the target primary
// key's during resolution -- and an ir.Relation carrying the association
// metadata. Phase 1 exposes only the scalar (spec §6.9.2 reserves the
// relation's own name for phase 1.5's expansion), so the field's own Type is
// already the right answer and reading the target entity here would be a
// second, drifting source for it.
func modelImports(e *ir.Entity) ([]string, error) {
	seen := make(map[string]bool)
	for _, f := range e.Fields {
		paths, err := importsForFieldType(f.Type)
		if err != nil {
			return nil, fmt.Errorf("gen: entity %q, field %q: %w", e.Name, f.Name.Value, err)
		}
		for _, p := range paths {
			seen[p] = true
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	// Sorted before it leaves the function: this is derived from a map, and
	// a map's iteration order is randomised per process (spec §5.3).
	sort.Strings(out)
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// importsForFieldType returns the packages a field of type t needs in the
// model package, per spec §3.2's type table.
//
// Every known FieldType is listed explicitly, on both sides of the
// needs-an-import line, so that adding a type to ir without deciding its
// import fails here rather than silently emitting a file that does not
// compile. An out-of-range value is an error, never an empty set: an empty
// set is what a correct answer looks like for half the table, so returning
// it for an unknown type would be a plausible-looking wrong answer.
func importsForFieldType(t ir.FieldType) ([]string, error) {
	switch t {
	case ir.FieldTypeUUID, ir.FieldTypeDecimal:
		// pgtype.UUID and pgtype.Numeric.
		return []string{pgtypeImport}, nil
	case ir.FieldTypeTimestamp, ir.FieldTypeDate:
		// time.Time for both; date carries no separate Go type (spec §3.2).
		return []string{"time"}, nil
	case ir.FieldTypeJSON:
		// json.RawMessage.
		return []string{"encoding/json"}, nil
	case ir.FieldTypeString, ir.FieldTypeText, ir.FieldTypeInt, ir.FieldTypeBigint,
		ir.FieldTypeFloat, ir.FieldTypeBool, ir.FieldTypeEnum:
		// string, int32, int64, float64, bool, and a generated string type.
		return nil, nil
	default:
		return nil, fmt.Errorf("no import rule for %s", t)
	}
}
