package gen

import (
	"reflect"
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
)

// TestPlan_Full pins the whole plan for the full fixture: every path, the
// package each file declares, the template that renders it and -- the part
// that matters most -- the exact import set.
//
// The import set is compared whole because it is authoritative. The
// formatter runs with FormatOnly and will not correct it (spec §5.2), so a
// wrong set here is a generated file that does not compile, and the only
// thing standing between the two is this assertion and spec §8's compile
// tier.
func TestPlan_Full(t *testing.T) {
	files, err := plan(loadFixture(t, "full"), testModulePath)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	hooksImports := []string{
		"context",
		modelImportPath(testModulePath),
		pgxImport,
		pgtypeImport,
	}
	want := []OutputFile{
		{
			Path:     "internal/gen/model/optional.go",
			Package:  "model",
			Imports:  []string{"encoding/json"},
			Template: "model/optional.go",
		},
		{
			Path:     "internal/gen/model/article.go",
			Package:  "model",
			Imports:  []string{"encoding/json", "github.com/jackc/pgx/v5/pgtype", "time"},
			Template: "model/entity.go",
		},
		{
			Path:     "internal/gen/model/user.go",
			Package:  "model",
			Imports:  []string{"github.com/jackc/pgx/v5/pgtype"},
			Template: "model/entity.go",
		},
		{
			Path:     "internal/gen/hooks/error.go",
			Package:  "hooks",
			Imports:  []string{"fmt", "net/http"},
			Template: "hooks/error.go",
		},
		{
			Path:     "internal/gen/hooks/article.go",
			Package:  "hooks",
			Imports:  hooksImports,
			Template: "hooks/entity.go",
		},
		{
			Path:     "internal/gen/hooks/user.go",
			Package:  "hooks",
			Imports:  hooksImports,
			Template: "hooks/entity.go",
		},
	}

	if len(files) != len(want) {
		t.Fatalf("plan returned %d files, want %d: %v", len(files), len(want), files)
	}
	for i, got := range files {
		w := want[i]
		if got.Path != w.Path {
			t.Errorf("file %d: Path = %q, want %q", i, got.Path, w.Path)
		}
		if got.Package != w.Package {
			t.Errorf("file %d (%s): Package = %q, want %q", i, w.Path, got.Package, w.Package)
		}
		if got.Template != w.Template {
			t.Errorf("file %d (%s): Template = %q, want %q", i, w.Path, got.Template, w.Template)
		}
		if !reflect.DeepEqual(got.Imports, w.Imports) {
			t.Errorf("file %d (%s): Imports = %v, want %v", i, w.Path, got.Imports, w.Imports)
		}
	}
}

// TestPlan_OrderFollowsTheSortedEntities pins that the plan's order is the
// schema's entity order, which ir.Schema.Freeze guarantees is sorted by
// name. Generate returns a map, so this order does not reach the output on
// its own -- but the plan is what a later step iterates to write files, and
// an unstable plan makes unstable diagnostics (spec §5.3).
func TestPlan_OrderFollowsTheSortedEntities(t *testing.T) {
	files, err := plan(loadFixture(t, "full"), testModulePath)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}

	got := make([]string, len(files))
	for i, f := range files {
		got[i] = f.Path
	}
	want := []string{
		"internal/gen/model/optional.go",
		"internal/gen/model/article.go",
		"internal/gen/model/user.go",
		"internal/gen/hooks/error.go",
		"internal/gen/hooks/article.go",
		"internal/gen/hooks/user.go",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan order = %v, want %v", got, want)
	}
}

// TestPlan_NilSchema pins the error text for a nil schema reaching plan
// directly. Generate rejects nil before it gets here, so this is the
// belt-and-braces half: an error string is part of the contract (CLAUDE.md).
func TestPlan_NilSchema(t *testing.T) {
	files, err := plan(nil, testModulePath)
	if files != nil {
		t.Errorf("plan(nil, testModulePath) returned %d files, want none", len(files))
	}
	if err == nil {
		t.Fatal("plan(nil, testModulePath) returned a nil error, want one")
	}
	const want = "gen: plan called on a nil schema"
	if err.Error() != want {
		t.Errorf("plan(nil, testModulePath) error = %q, want %q", err.Error(), want)
	}
}

// TestImportsForFieldType covers every FieldType spec §3.2 defines, on both
// sides of the needs-an-import line, so that adding a type to internal/ir
// without deciding its import fails here.
func TestImportsForFieldType(t *testing.T) {
	cases := []struct {
		t    ir.FieldType
		want []string
	}{
		{ir.FieldTypeUUID, []string{"github.com/jackc/pgx/v5/pgtype"}},
		{ir.FieldTypeString, nil},
		{ir.FieldTypeText, nil},
		{ir.FieldTypeInt, nil},
		{ir.FieldTypeBigint, nil},
		{ir.FieldTypeFloat, nil},
		{ir.FieldTypeDecimal, []string{"github.com/jackc/pgx/v5/pgtype"}},
		{ir.FieldTypeBool, nil},
		{ir.FieldTypeTimestamp, []string{"time"}},
		{ir.FieldTypeDate, []string{"time"}},
		{ir.FieldTypeJSON, []string{"encoding/json"}},
		{ir.FieldTypeEnum, nil},
	}

	for _, c := range cases {
		t.Run(c.t.String(), func(t *testing.T) {
			got, err := importsForFieldType(c.t)
			if err != nil {
				t.Fatalf("importsForFieldType(%s): %v", c.t, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("importsForFieldType(%s) = %v, want %v", c.t, got, c.want)
			}
		})
	}

	if len(cases) != int(ir.FieldTypeEnum)+1 {
		t.Errorf("this test covers %d FieldTypes but the enumeration has %d: a type was added to "+
			"internal/ir without an import rule", len(cases), int(ir.FieldTypeEnum)+1)
	}
}

// TestImportsForFieldType_Unknown pins that an out-of-range FieldType is an
// error and not an empty set.
//
// An empty set is what a CORRECT answer looks like for seven of the twelve
// types, so returning it for an unknown one would be a plausible-looking
// wrong answer -- a file silently missing an import, reported by the Go
// compiler at best and by a user at worst.
func TestImportsForFieldType_Unknown(t *testing.T) {
	got, err := importsForFieldType(ir.FieldType(99))
	if got != nil {
		t.Errorf("importsForFieldType(FieldType(99)) = %v, want no paths", got)
	}
	if err == nil {
		t.Fatal("importsForFieldType(FieldType(99)) returned a nil error, want one")
	}
	const want = "no import rule for FieldType(99)"
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}

// TestModelImports_NoImports pins that an entity needing nothing gets a nil
// import set, not an empty non-nil one -- importBlock renders nothing at all
// for it, and an "import ()" in a generated file is not something a user
// should ever be handed.
func TestModelImports_NoImports(t *testing.T) {
	e := &ir.Entity{
		Name:   "note",
		GoName: "Note",
		Table:  "notes",
		Fields: []*ir.Field{
			{Name: at("id"), GoName: "ID", Column: "id", Type: ir.FieldTypeBigint, PK: true},
			{Name: at("body"), GoName: "Body", Column: "body", Type: ir.FieldTypeText},
		},
	}

	got, err := modelImports(e)
	if err != nil {
		t.Fatalf("modelImports: %v", err)
	}
	if got != nil {
		t.Errorf("modelImports = %v, want nil", got)
	}
}

// TestModelImports_Deduplicates pins that two fields wanting the same
// package produce one import, and that the result is sorted -- it is derived
// from a map, whose iteration order Go randomises per process (spec §5.3).
func TestModelImports_Deduplicates(t *testing.T) {
	e := &ir.Entity{
		Name:   "thing",
		GoName: "Thing",
		Table:  "things",
		Fields: []*ir.Field{
			{Name: at("updated_at"), GoName: "UpdatedAt", Column: "updated_at", Type: ir.FieldTypeTimestamp},
			{Name: at("id"), GoName: "ID", Column: "id", Type: ir.FieldTypeUUID, PK: true},
			{Name: at("created_at"), GoName: "CreatedAt", Column: "created_at", Type: ir.FieldTypeTimestamp},
		},
	}

	got, err := modelImports(e)
	if err != nil {
		t.Fatalf("modelImports: %v", err)
	}
	want := []string{"github.com/jackc/pgx/v5/pgtype", "time"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modelImports = %v, want %v", got, want)
	}
}

// TestModelImports_UnknownFieldTypeNamesTheField pins that an unknown type
// is reported with the entity and field it came from, not as a bare
// complaint about an integer.
func TestModelImports_UnknownFieldTypeNamesTheField(t *testing.T) {
	e := &ir.Entity{
		Name:   "thing",
		GoName: "Thing",
		Table:  "things",
		Fields: []*ir.Field{
			{Name: at("weird"), GoName: "Weird", Column: "weird", Type: ir.FieldType(99)},
		},
	}

	got, err := modelImports(e)
	if got != nil {
		t.Errorf("modelImports = %v, want nil", got)
	}
	if err == nil {
		t.Fatal("modelImports returned a nil error for an unknown field type, want one")
	}
	const want = `gen: entity "thing", field "weird": no import rule for FieldType(99)`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err.Error(), want)
	}
}
