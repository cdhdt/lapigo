package validate

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/parse"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestValidate_SpecWorkedExample reproduces spec §4.1's own worked example
// byte for byte, against a literal Go string constant rather than only the
// golden file on disk -- the golden file could drift from the spec text
// without either the test or a reviewer noticing, since -update would
// happily re-approve a wrong rendering. This test pins the contract
// directly: "Every error carries file, line, column, a caret span and an
// actionable hint" (spec §4.1), demonstrated with exactly the sort-key-
// uniqueness diagnostic the spec itself uses as its example.
func TestValidate_SpecWorkedExample(t *testing.T) {
	const want = "lapigo.yaml:12:12: sort key \"created_at\" is not unique\n" +
		"   12 |     sort: [-created_at]\n" +
		"      |            ^^^^^^^^^^^\n" +
		"   the last sort key must be unique; add a second key such as `-id`,\n" +
		"   or mark `created_at` unique"

	f := readFixture(t, "sort_key_not_unique")
	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("fixture must parse cleanly; got: %v", parseDiags)
	}

	diags := Validate(schema, f.Name)
	if got := diags.Render(f); got != want {
		t.Errorf("Render() =\n%s\nwant (spec §4.1's own worked example):\n%s", got, want)
	}
}

// TestValidate_WarningDoesNotBlockGeneration is the regression test for spec
// §3.3 rule 5 and diag.Diagnostics' own contract: a mutable sort key is a
// warning, and Diagnostics.Err (the accessor generation actually calls, per
// diag's doc comment on Err vs StrictErr) must return nil when every
// diagnostic present is a Warning, even though the accumulator is non-empty.
// Asserting only `len(diags) == 0` here would be the wrong test -- the whole
// point of this fixture is that diags is *not* empty, and generation must
// proceed anyway.
func TestValidate_WarningDoesNotBlockGeneration(t *testing.T) {
	f := readFixture(t, "sort_key_mutable_warning")
	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("fixture must parse cleanly; got: %v", parseDiags)
	}

	diags := Validate(schema, f.Name)

	if len(diags) == 0 {
		t.Fatal("want at least one diagnostic (the mutability warning); got none")
	}
	if diags.HasErrors() {
		t.Errorf("HasErrors() = true, want false: a warning-only accumulator must not read as a hard failure\n%s", diags.Render(f))
	}
	if err := diags.Err(); err != nil {
		t.Errorf("Err() = %v, want nil: a warning-only accumulator must not stop generation", err)
	}
	for _, d := range diags {
		if d.Severity != diag.Warning {
			t.Errorf("diagnostic %+v has Severity %v, want Warning", d, d.Severity)
		}
	}
}

// TestValidate_ErrorFixtureHasErrors is the mirror check for
// TestValidate_WarningDoesNotBlockGeneration: an error-severity rule
// (sort-key uniqueness here) must make HasErrors true and Err non-nil, so
// the two severities are never accidentally swapped -- CLAUDE.md's own
// warning about this exact class of bug: "Getting this backwards either
// blocks a legitimate schema or lets a broken one through."
func TestValidate_ErrorFixtureHasErrors(t *testing.T) {
	f := readFixture(t, "sort_key_not_unique")
	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("fixture must parse cleanly; got: %v", parseDiags)
	}

	diags := Validate(schema, f.Name)
	if !diags.HasErrors() {
		t.Fatalf("HasErrors() = false, want true for a sort-key-uniqueness violation\n%s", diags.Render(f))
	}
	if diags.Err() == nil {
		t.Error("Err() = nil, want non-nil for a schema with an Error-severity diagnostic")
	}
}

// TestValidate_SortKeyNotUnique_ExactFields asserts every field of the
// single Diagnostic sort_key_not_unique.yaml produces, by value, not through
// the rendered string -- CLAUDE.md: "Compare the whole output to a literal,
// especially where the output is a contract." Render's own formatting is
// covered by TestValidate_SpecWorkedExample; this test instead pins the
// Diagnostic struct's fields directly, so a bug in Render could not mask a
// wrong Pos/EndColumn/Severity here.
func TestValidate_SortKeyNotUnique_ExactFields(t *testing.T) {
	f := readFixture(t, "sort_key_not_unique")
	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("fixture must parse cleanly; got: %v", parseDiags)
	}

	diags := Validate(schema, f.Name)
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	got := diags[0]
	want := diag.Diagnostic{
		Severity:  diag.Error,
		File:      "lapigo.yaml",
		Pos:       source.Pos{Line: 12, Column: 12},
		EndColumn: 23,
		Message:   `sort key "created_at" is not unique`,
		Hint:      "the last sort key must be unique; add a second key such as `-id`,\nor mark `created_at` unique",
	}
	if got != want {
		t.Errorf("Validate() diagnostic =\n%+v\nwant:\n%+v", got, want)
	}
}

// TestValidate_EntityNameCollision_ExactFields pins the entity/entity
// collision diagnostic's exact fields: it blames Entity.NameSpan, the
// colliding entity's own declared name in `entities:`, not any field inside
// it (see schemaPackageDecls' doc comment).
func TestValidate_EntityNameCollision_ExactFields(t *testing.T) {
	f := readFixture(t, "entity_name_collision")
	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("fixture must parse cleanly; got: %v", parseDiags)
	}

	diags := Validate(schema, f.Name)
	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	got := diags[0]
	want := diag.Diagnostic{
		Severity:  diag.Error,
		File:      "lapigo.yaml",
		Pos:       source.Pos{Line: 2, Column: 3},
		EndColumn: 15,
		Message:   `entity "userProfile" and entity "user_profile" both produce Go identifier "UserProfile"`,
		Hint:      "rename one of them so their generated Go identifiers don't collide (spec §5.6)",
	}
	if got != want {
		t.Errorf("Validate() diagnostic =\n%+v\nwant:\n%+v", got, want)
	}
}

// TestValidate_DeterministicAcrossRepeatedCalls runs Validate several times
// on the same schema and asserts byte-identical rendered output every time
// (CLAUDE.md's determinism rule, spec §5.3's "same schema in, byte-identical
// out" applied to diagnostics rather than generated files). This package
// builds no vocabulary from a Go map that any diagnostic's order or content
// depends on -- reservedMethodNameSet is only ever tested for membership,
// never ranged -- so, unlike internal/parse's own
// TestParse_SuggestionIsDeterministicAcrossProcesses, there is no
// process-randomised map order for a single-process repeated-call test to
// fail to catch; this test still exists as the cheap, always-worth-having
// half of that guarantee.
func TestValidate_DeterministicAcrossRepeatedCalls(t *testing.T) {
	f := readFixture(t, "valid")
	schema, parseDiags := parse.Parse(f)
	if len(parseDiags) != 0 {
		t.Fatalf("fixture must parse cleanly; got: %v", parseDiags)
	}

	// enum_type_collision exercises the schema-wide pass with the richest
	// mix of entities and fields among this package's fixtures that still
	// produces diagnostics, so it is used here instead of valid.yaml (which
	// produces none, and so cannot distinguish a correct render from a
	// silently reordered one).
	f2 := readFixture(t, "enum_type_collision")
	schema2, parseDiags2 := parse.Parse(f2)
	if len(parseDiags2) != 0 {
		t.Fatalf("fixture must parse cleanly; got: %v", parseDiags2)
	}

	var first string
	for i := 0; i < 20; i++ {
		got := Validate(schema2, f2.Name).Render(f2)
		if i == 0 {
			first = got
			continue
		}
		if got != first {
			t.Fatalf("run %d differs from run 0:\nrun 0: %s\nrun %d: %s", i, first, i, got)
		}
	}

	// valid.yaml's zero-diagnostic result must also stay stable.
	for i := 0; i < 20; i++ {
		if diags := Validate(schema, f.Name); len(diags) != 0 {
			t.Fatalf("run %d: want zero diagnostics for valid.yaml, got %+v", i, diags)
		}
	}
}

// TestValidate_NilSchemaReturnsNoDiagnostics is the regression test for the
// review-found panic: Validate used to dereference schema.Entities with no
// nil guard, while ir.Schema.Lookup makes the explicit effort to be
// nil-safe. A nil *ir.Schema is not a hypothetical caller error -- it is
// exactly the value parse.Parse hands back alongside a non-empty
// diag.Diagnostics whenever parsing itself fails (see
// TestValidate_SchemaThatFailedToParseDoesNotPanic below for the reachable
// path). Validate(nil, ...) must return nil, not panic.
func TestValidate_NilSchemaReturnsNoDiagnostics(t *testing.T) {
	diags := Validate(nil, "lapigo.yaml")
	if diags != nil {
		t.Errorf("Validate(nil, \"lapigo.yaml\") = %+v, want nil", diags)
	}
}

// TestValidate_SchemaThatFailedToParseDoesNotPanic reproduces the exact
// repro from the review: `entities: 3` is a schema file whose top-level
// `entities:` value is an integer, not a mapping, so parse.Parse reports one
// diagnostic ("`entities` must be a mapping, found an integer") and returns
// a nil *ir.Schema. A caller that forwards that nil schema straight into
// Validate -- an entirely plausible pipeline shape -- used to panic instead
// of returning cleanly.
func TestValidate_SchemaThatFailedToParseDoesNotPanic(t *testing.T) {
	schema, parseDiags := parse.Parse(source.File{Name: "lapigo.yaml", Src: []byte("entities: 3\n")})
	if schema != nil {
		t.Fatalf("parse.Parse(\"entities: 3\") returned a non-nil schema: %+v", schema)
	}
	if len(parseDiags) != 1 {
		t.Fatalf("parse.Parse(\"entities: 3\") diagnostics = %+v, want exactly 1", parseDiags)
	}

	diags := Validate(schema, "lapigo.yaml")
	if diags != nil {
		t.Errorf("Validate(nil, ...) = %+v, want nil", diags)
	}
}
