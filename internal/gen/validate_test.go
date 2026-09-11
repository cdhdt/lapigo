package gen

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/parse"
	"github.com/cdhdt/lapigo/internal/source"
	"github.com/cdhdt/lapigo/internal/validate"
)

// widgetEntityForValidateTests is a hand-built IR entity (not a testdata
// fixture) exercising every shape validateMembersFor has to distinguish:
//
//   - name: required, max: 5 -- mandatory AND a length check.
//   - status: required enum -- mandatory AND an enum-membership check.
//   - category: required with a default -- NOT mandatory (the default
//     supplies it), but the column is still NOT NULL, so an explicit null
//     must still be rejected (spec §3.1, §6.5's "default: means supplied at
//     insert, not never settable").
//   - note: optional (nullable), max: 3 -- a length check with no
//     mandatory or null-rejection test at all: null is a legitimate value.
//   - body: optional (nullable), no max, not enum -- nothing to check,
//     and therefore absent from the returned slice entirely.
//   - slug: readonly -- Hidden, off the wire entirely; must never
//     contribute a member, since Validate runs before any hook could set it
//     (spec §6.5's decode-Validate-transaction-BeforeX order).
//   - id: pk, version: the version column -- both Absent.
//
// The same Fields slice is reused for CreateInput and UpdateInput: nothing
// here is immutable, so both input types project the same visible set, and
// the only difference the two accessors must produce is IsCreate (spec
// §6.5's differing "missing" test) and the entity's HasCreate/HasUpdate
// gating, which is plan.go's concern, not this function's.
func widgetEntityForValidateTests() *ir.Entity {
	maxName := 5
	maxNote := 3
	return &ir.Entity{
		Name:   "widget",
		GoName: "Widget",
		Table:  "widgets",
		Fields: []*ir.Field{
			{Name: at("id"), GoName: "ID", Column: "id", Type: ir.FieldTypeUUID, PK: true},
			{Name: at("name"), GoName: "Name", Column: "name", Type: ir.FieldTypeString, Nullable: false, Max: &maxName},
			{Name: at("status"), GoName: "Status", Column: "status", Type: ir.FieldTypeEnum, Nullable: false,
				EnumGoType: "WidgetStatus",
				EnumValues: []ir.EnumValue{
					{Name: at("draft"), GoName: "Draft"},
					{Name: at("published"), GoName: "Published"},
				},
			},
			{Name: at("category"), GoName: "Category", Column: "category", Type: ir.FieldTypeString, Nullable: false,
				Default: &ir.DefaultValue{Kind: ir.DefaultLiteral, Literal: "general"}},
			{Name: at("note"), GoName: "Note", Column: "note", Type: ir.FieldTypeString, Nullable: true, Max: &maxNote},
			{Name: at("body"), GoName: "Body", Column: "body", Type: ir.FieldTypeText, Nullable: true},
			{Name: at("slug"), GoName: "Slug", Column: "slug", Type: ir.FieldTypeString, ReadOnly: true, Nullable: true},
			{Name: at("version"), GoName: "Version", Column: "version", Type: ir.FieldTypeInt, Version: true, Nullable: false},
		},
		Endpoints: []ir.Endpoint{{Kind: ir.EndpointCreate}, {Kind: ir.EndpointUpdate}},
	}
}

// TestCreateValidateMembers pins the exact member list createValidateMembers
// produces for widgetEntityForValidateTests, field by field. Asserted whole,
// not with a length check or a substring: CLAUDE.md's "assert on exact
// values" rule exists precisely because a relational or partial assertion
// here would pass with the mandatory/nullcheck table silently swapped.
func TestCreateValidateMembers(t *testing.T) {
	got := createValidateMembers(widgetEntityForValidateTests())

	want := []validateMember{
		{
			GoName: "Name", Column: "name", IsCreate: true, Mandatory: true,
			HasMax: true, Max: 5, MaxMessage: "must be at most 5 characters",
		},
		{
			GoName: "Status", Column: "status", IsCreate: true, Mandatory: true,
			IsEnum: true, EnumGoType: "WidgetStatus",
			EnumValues: []ir.EnumValue{
				{Name: at("draft"), GoName: "Draft"},
				{Name: at("published"), GoName: "Published"},
			},
			EnumMessage: "must be one of: draft, published",
		},
		{
			GoName: "Category", Column: "category", IsCreate: true, Mandatory: false, NullCheck: true,
		},
		{
			GoName: "Note", Column: "note", IsCreate: true, Mandatory: false, NullCheck: false,
			HasMax: true, Max: 3, MaxMessage: "must be at most 3 characters",
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("createValidateMembers = %#v, want %#v", got, want)
	}
}

// TestUpdateValidateMembers pins the same matrix for UpdateInput, whose only
// difference from Create's is IsCreate -- the "missing" test's shape, spec
// §6.5's "!Present() || IsNull() on CreateInput; IsNull() alone on
// UpdateInput". Nothing in widgetEntityForValidateTests is immutable, so the
// visible set itself is identical between the two.
func TestUpdateValidateMembers(t *testing.T) {
	got := updateValidateMembers(widgetEntityForValidateTests())

	want := []validateMember{
		{
			GoName: "Name", Column: "name", IsCreate: false, Mandatory: true,
			HasMax: true, Max: 5, MaxMessage: "must be at most 5 characters",
		},
		{
			GoName: "Status", Column: "status", IsCreate: false, Mandatory: true,
			IsEnum: true, EnumGoType: "WidgetStatus",
			EnumValues: []ir.EnumValue{
				{Name: at("draft"), GoName: "Draft"},
				{Name: at("published"), GoName: "Published"},
			},
			EnumMessage: "must be one of: draft, published",
		},
		{
			GoName: "Category", Column: "category", IsCreate: false, Mandatory: false, NullCheck: true,
		},
		{
			GoName: "Note", Column: "note", IsCreate: false, Mandatory: false, NullCheck: false,
			HasMax: true, Max: 3, MaxMessage: "must be at most 3 characters",
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("updateValidateMembers = %#v, want %#v", got, want)
	}
}

// TestValidateMembersFor_SkipsHiddenAndAbsentMembers pins that pk, version
// and readonly fields never reach the returned slice at all -- not even as a
// no-op entry. IsMandatory documents itself as moot, not wrong, for a field
// whose presence is not ir.PresenceVisible (a readonly member's value is set
// by a hook that runs AFTER Validate, per spec §6.5's decode-Validate-
// transaction-BeforeX order), and this is the check that keeps that
// distinction from collapsing into a required-check on a primary key.
func TestValidateMembersFor_SkipsHiddenAndAbsentMembers(t *testing.T) {
	e := widgetEntityForValidateTests()
	for _, members := range [][]validateMember{createValidateMembers(e), updateValidateMembers(e)} {
		for _, m := range members {
			if m.GoName == "ID" || m.GoName == "Version" || m.GoName == "Slug" {
				t.Errorf("validateMembersFor produced a member for %q, which must be absent or hidden", m.GoName)
			}
		}
	}
}

// TestValidateMembersFor_ExcludesFieldsWithNoCheckAtAll pins that "body" --
// optional, nullable, no max, not enum -- contributes no entry: there is
// nothing Validate could ever say about it, so the member list, and
// therefore the generated method, carries no dead branch for it.
func TestValidateMembersFor_ExcludesFieldsWithNoCheckAtAll(t *testing.T) {
	e := widgetEntityForValidateTests()
	for _, members := range [][]validateMember{createValidateMembers(e), updateValidateMembers(e)} {
		for _, m := range members {
			if m.GoName == "Body" {
				t.Errorf("validateMembersFor produced a member for \"Body\", which has nothing to check")
			}
		}
	}
}

// validateRunSchema is the schema TestGeneratedValidate_Runs compiles and
// executes for real, chosen to reach every check Validate emits through a
// real json.Unmarshal, not a hand-built Optional[T]:
//
//   - name: required, max: 5 -- mandatory member AND a length check.
//   - status: required enum -- mandatory member AND an enum-membership check.
//   - category: required with a default -- optional (the default supplies
//     it at insert), but the column is NOT NULL, so an explicit null must
//     still be rejected (spec §3.1, §6.5).
//   - note: exists only so the schema is not suspiciously minimal; not
//     exercised by name in the transcript.
//
// It is parsed from inline YAML with the real pipeline (parse.Parse,
// validate.Validate, Schema.Freeze) rather than a testdata/*.yaml fixture,
// so it needs no entry in goldenCases and is invisible to
// TestGolden_NoOrphanFixtures -- this schema's output is never compared
// byte for byte, only compiled and run.
func validateRunSchema(t *testing.T) *ir.Schema {
	t.Helper()

	const yaml = `entities:
  widget:
    fields:
      id:       { type: uuid, pk: true }
      name:     { type: string, required: true, max: 5 }
      status:   { type: enum, values: [draft, published], required: true }
      category: { type: string, required: true, default: general }
      note:     { type: string, max: 3 }
    endpoints: [create, update]
`
	f := source.File{Name: "lapigo.yaml", Src: []byte(yaml)}
	schema, diags := parse.Parse(f)
	if len(diags) != 0 {
		t.Fatalf("validateRunSchema must parse cleanly; got:\n%s", diags.Render(f))
	}
	vdiags := validate.Validate(schema, f.Name)
	if err := vdiags.Err(); err != nil {
		t.Fatalf("validateRunSchema must carry no error-severity diagnostics; got:\n%s", vdiags.Render(f))
	}
	if err := schema.Freeze(); err != nil {
		t.Fatalf("validateRunSchema must freeze: %v", err)
	}
	return schema
}

// harnessSrc is a small hand-written program, not generated by this
// package, that decodes real JSON request bodies into the generated
// WidgetCreateInput/WidgetUpdateInput, calls the generated Validate, and
// prints one line per case. It is the "run it" half of issue #28's
// requirement: a rendered template that type-checks is not evidence that
// Validate does what spec §6.5 says, only that it compiles.
//
// %[1]s is the generated model package's full import path, which depends on
// the module path TestGeneratedValidate_Runs generates under.
const harnessSrc = `package main

import (
	"encoding/json"
	"fmt"
	"os"

	"%[1]s"
)

var ok = true

// check runs in.Validate and compares it against wantErr: "" for a nil
// error, or fmt's own (deterministically key-sorted) %%v rendering of a
// *model.ValidationError's Fields for anything else.
func check(name string, in interface{ Validate() error }, wantErr string) {
	err := in.Validate()
	switch {
	case wantErr == "" && err == nil:
		fmt.Printf("%%s: ok, Validate() = nil\n", name)
	case wantErr == "" && err != nil:
		fmt.Printf("%%s: Validate() = %%v, want nil\n", name, err)
		ok = false
	case wantErr != "" && err == nil:
		fmt.Printf("%%s: Validate() = nil, want an error\n", name)
		ok = false
	default:
		ve, isVE := err.(*model.ValidationError)
		if !isVE {
			fmt.Printf("%%s: error type = %%T, want *model.ValidationError\n", name, err)
			ok = false
			return
		}
		got := fmt.Sprintf("%%v", ve.Fields)
		if got != wantErr {
			fmt.Printf("%%s: Fields = %%s, want %%s\n", name, got, wantErr)
			ok = false
			return
		}
		fmt.Printf("%%s: ok, Fields = %%s\n", name, got)
	}
}

func decodeCreate(body string) *model.WidgetCreateInput {
	var in model.WidgetCreateInput
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		panic(err)
	}
	return &in
}

func decodeUpdate(body string) *model.WidgetUpdateInput {
	var in model.WidgetUpdateInput
	if err := json.Unmarshal([]byte(body), &in); err != nil {
		panic(err)
	}
	return &in
}

func main() {
	check("missing mandatory member",
		decodeCreate(` + "`" + `{"status":"published"}` + "`" + `),
		"map[name:required]")

	check("over-length string",
		decodeCreate(` + "`" + `{"name":"toolong","status":"draft"}` + "`" + `),
		"map[name:must be at most 5 characters]")

	check("enum value outside the set",
		decodeCreate(` + "`" + `{"name":"ok","status":"bogus"}` + "`" + `),
		"map[status:must be one of: draft, published]")

	check("explicit null on a non-nullable member",
		decodeCreate(` + "`" + `{"name":"ok","status":"draft","category":null}` + "`" + `),
		"map[category:must not be null]")

	check("fully valid input",
		decodeCreate(` + "`" + `{"name":"ok","status":"draft"}` + "`" + `),
		"")

	check("update: absence of a mandatory member is unchanged, not an error",
		decodeUpdate(` + "`" + `{}` + "`" + `),
		"")

	check("update: explicit null on a mandatory member is rejected",
		decodeUpdate(` + "`" + `{"name":null}` + "`" + `),
		"map[name:required]")

	if !ok {
		os.Exit(1)
	}
}
`

// TestGeneratedValidate_Runs compiles validateRunSchema's generated model
// package for real and executes harnessSrc against it -- CLAUDE.md's "never
// claim work is complete without running the verification and reading the
// output", applied to a template rather than a hand-written function. It
// reuses compile_test.go's writeTempModule and compileModule so that the
// generated package resolves against the same pgx and the same go.sum this
// repository already builds with, and stays offline the same way that test
// does.
//
// Every one of the five checks issue #28 asks for is here: a missing
// mandatory member, an over-length string, an enum value outside the
// declared set, an explicit null on a non-nullable member, and a fully
// valid input -- plus two more exercising UpdateInput's differing "missing"
// semantics (spec §6.5, §6.6), which CreateInput's own shape cannot reach.
func TestGeneratedValidate_Runs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping: it shells out to `go run`")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain on PATH: %v", err)
	}

	dir := t.TempDir()
	writeTempModule(t, dir)

	const sub = "validaterun"
	files, err := Generate(validateRunSchema(t), compileModule+"/"+sub)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for p, src := range files {
		dst := filepath.Join(dir, sub, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	modelImport := compileModule + "/" + sub + "/" + genRoot + "/" + packageModel
	harnessDir := filepath.Join(dir, sub, "cmd", "harness")
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		t.Fatal(err)
	}
	harness := fmt.Sprintf(harnessSrc, modelImport)
	if err := os.WriteFile(filepath.Join(harnessDir, "main.go"), []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("go", "run", "./"+sub+"/cmd/harness")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	t.Logf("harness transcript:\n%s", out)
	if err != nil {
		t.Fatalf("the generated validator failed one or more checks: %v\n%s", err, out)
	}

	want := strings.Join([]string{
		"missing mandatory member: ok, Fields = map[name:required]",
		"over-length string: ok, Fields = map[name:must be at most 5 characters]",
		"enum value outside the set: ok, Fields = map[status:must be one of: draft, published]",
		"explicit null on a non-nullable member: ok, Fields = map[category:must not be null]",
		"fully valid input: ok, Validate() = nil",
		"update: absence of a mandatory member is unchanged, not an error: ok, Validate() = nil",
		"update: explicit null on a mandatory member is rejected: ok, Fields = map[name:required]",
		"",
	}, "\n")
	if string(out) != want {
		t.Errorf("harness transcript =\n%s\nwant:\n%s", out, want)
	}
}
