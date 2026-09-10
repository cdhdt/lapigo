package validate

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/parse"
	"github.com/cdhdt/lapigo/internal/source"
)

// update regenerates every golden .diag file from the diagnostics Validate
// actually produces, rather than comparing against what is already on disk.
// `go test ./internal/validate/... -run TestValidate_Golden -update` after a
// deliberate wording change; the resulting diff to testdata/ is what gets
// reviewed (CLAUDE.md's golden-file testing convention; internal/parse's
// golden_test.go does the same thing).
var update = flag.Bool("update", false, "update golden .diag files")

// goldenCases lists every table-driven diagnostic fixture: a .yaml file
// under testdata/ that internal/parse must accept cleanly (a non-nil
// *ir.Schema, zero parse diagnostics -- if it doesn't, the fixture is
// testing internal/parse, not this package), and the .diag file holding the
// exact text Validate's diagnostics are expected to render to. One entry per
// rule this package enforces, so a rule tested only by its own success case
// is not silently trusted to also reject what it should.
var goldenCases = []string{
	"sort_key_not_unique", // spec §4.1's worked example, reproduced byte for byte
	"sort_key_nullable",
	"sort_key_decimal",
	"sort_key_json",
	"sort_key_mutable_warning", // a warning, not an error -- see TestValidate_WarningDoesNotBlockGeneration

	// version_sort_key_mutable_warning is issue #46's trap check: a
	// version column the server bumps on every write is the worst possible
	// sort key, so validateSortKeyMutability must keep warning on it even
	// after resolveVersion (internal/parse/schema.go) starts requiring
	// `required: true` and rejecting a nullable/wrong-typed/PK version
	// field. The fix for issue #46 must NOT synthesize ReadOnly on the
	// version field or add a Version case to validateSortKeyMutability's
	// exemption list -- either would make this fixture stop warning
	// silently. Only "version" warns here; "id" is the PK and stays exempt.
	"version_sort_key_mutable_warning",
	// sort_key_readonly_exempt_default_warns is issue #45's regression: a
	// `default:` field is settable on update (spec §3.1/§6.5, decided in
	// #16) and so is exactly what rule 5 exists to catch -- it must warn
	// like any other mutable sort key, not be exempted. This fixture (née
	// sort_key_default_and_readonly_exempt, when `default:` alone was still
	// exempt and it lived in successFixtures) keeps both non-last keys from
	// that original case: "created_at" carries only `default: now` and now
	// expects the warning, while "slug" carries only `readonly: true` and
	// stays silent, so the ReadOnly exemption -- untouched by this issue,
	// and otherwise untested outside of valid.yaml's non-sort-key "slug" --
	// is still proven to hold on a non-last key. The last key, "id", is the
	// pk tiebreaker and stays silent via the PK exemption.
	"sort_key_readonly_exempt_default_warns",

	"filter_json",

	// filter_reserved_limit and filter_reserved_after are issue #53: spec
	// §6.9.4 reserves the query-parameter names "limit" and "after" for
	// pagination, so a filter whose wire name (Field.Column, per §6.9.2) is
	// either would be silently shadowed by pagination -- the request would
	// either filter on nothing or break pagination, depending on parse
	// order. Both fixtures declare a *scalar* field named "limit"/"after",
	// whose column equals the field name (internal/parse/field.go), so the
	// collision is direct. filter_reserved_belongs_to_ok in successFixtures
	// below is the counterpart this rule needs: a belongsTo relation named
	// "limit" is legal, because its column is "limit_id", not "limit".
	"filter_reserved_limit",
	"filter_reserved_after",

	"field_method_collision",
	"relation_field_collision",
	"entity_name_collision",
	"enum_type_collision",

	// entity_order_field_method_collision is the mutation-gap regression for
	// Validate's per-entity loop (`for _, e := range schema.Entities`): the
	// violation lives on "zzz_widget", the *second* entity in sorted order,
	// with "article" clean ahead of it. A mutant that clips the loop to only
	// the first entity (e.g. schema.Entities[:1]) produces zero diagnostics
	// here, unlike every older fixture in this list, which all put their
	// fault in "article" -- the first entity -- and so cannot tell a full
	// scan from a truncated one.
	"entity_order_field_method_collision",

	// sort_key_nonlast_violation is the mutation-gap regression for
	// validateSort's per-key loop: both validateSortKeyEligibility and
	// validateSortKeyMutability must run for every key, not only the last
	// one (only validateSortKeyUniqueness is legitimately restricted to the
	// last key, per spec §3.3 rule 2). `sort: [col, id]` puts a nullable,
	// mutable field first and a clean pk tiebreaker last; a mutant that
	// restricts either check to the last key only drops one of the two
	// diagnostics this fixture expects on "col".
	"sort_key_nonlast_violation",

	// enum_field_collision is the mutation-gap regression for
	// schemaPackageDecls' enum-field packageDecl: it is the second ("later")
	// declaration of the pair here, unlike the older enum_type_collision
	// fixture where the enum field is always the earlier declaration and the
	// entity is always the later one. validatePackageNames only ever renders
	// the later declaration's own Pos/EndColumn, so this is the only fixture
	// that exercises the enum field's own span as the one actually rendered
	// in a diagnostic.
	//
	// Since schemaPackageDecls also emits one packageDecl per enum value
	// (packageDecl's own doc comment, review finding F1 on PR #39), the two
	// fields' identical EnumGoType collision here also drags every one of
	// their values into collision with each other -- "draft" against
	// "draft", "published" against "published" -- so this fixture's .diag
	// carries three diagnostics, not one.
	"enum_field_collision",

	// relation_method_collision is the mutation-gap regression for spec
	// §5.6's reserved-name check on the *relation* path through
	// entityMembers: field_method_collision only ever exercised a plain
	// field named "validate"; this fixture is a `belongsTo` relation named
	// "validate" instead.
	"relation_method_collision",

	// sort_key_nullable_decimal is the mutation-gap regression for
	// validateSortKeyEligibility's switch order: "price" is both nullable
	// and decimal, so IsSortEligible's own nullability-first check and this
	// function's switch must agree on which reason wins. A mutant that
	// checks the decimal case before nullability would render the wrong
	// message/hint for this fixture without IsSortEligible's return value
	// itself changing.
	"sort_key_nullable_decimal",

	// The declared-index rules of spec §3.5/§7.2: a column that is not a
	// declared filter, an entry naming one filter (the derived set already
	// covers single filters), a column named twice, and two entries naming
	// the same combination. declared_indexes_valid.yaml is the success
	// counterpart, including two entries over the same filters in different
	// orders -- genuinely different indexes, so both are legal.
	"index_column_not_a_filter",
	"index_single_filter",
	"index_duplicate_column",
	"index_duplicate_entry",

	// set_null_on_required is the DDL-driven relation rule: NOT NULL plus
	// ON DELETE SET NULL applies cleanly and then fails on every delete of
	// a referenced row. nullable_set_null in successFixtures is the legal
	// counterpart.
	"set_null_on_required",

	// entity_triple_collision is the mutation-gap regression for
	// validatePackageNames' own `seen` map: three entities named a_x, aX and
	// a__x (sorted by ir.Schema.Freeze into aX, a__x, a_x) all produce Go
	// identifier "AX" (goName strips underscores). The correct behaviour
	// keeps the *first* declared member ("aX", first in sorted order) as
	// "prior" for every subsequent collision; a mutant that overwrites
	// seen[goName] on every hit would instead report "a__x" as prior for the
	// third entity's diagnostic.
	//
	// validateEntityStructNames' own `seen` map (fields+relations within one
	// entity) has the identical pattern but no three-way collision can ever
	// reach it through parse.Parse: any two plain fields with a colliding
	// GoName are already rejected by internal/parse's own
	// checkFieldCollisions before an *ir.Schema exists, and two `belongsTo`
	// relations with a colliding GoName always also collide on their own
	// underlying FK field's GoName (goName(name)+"ID"), which
	// checkFieldCollisions catches for the same reason. The only pairing
	// that reaches validateEntityStructNames at all is exactly one plain
	// field against exactly one relation (relation_field_collision.yaml) --
	// there is no third member to add. TestValidateEntityStructNames_KeepsFirstPriorAcrossThreeWayCollision
	// in names_test.go covers this by calling validateEntityStructNames
	// directly on a hand-built *ir.Entity, the same way
	// TestValidateSortKeyEligibility_UnknownFieldType in sort_test.go
	// bypasses parse.Parse to reach a state parse.Parse itself would refuse
	// to construct.
	"entity_triple_collision",

	// enum_value_collision is issue #24's own fixture: two values of the
	// same enum field ("in-progress" and "in_progress") whose computed Go
	// identifiers collide on "InProgress". Before this fixture existed
	// (and before ir.Field.EnumValues carried a computed GoName at all),
	// this schema parsed and validated with zero diagnostics and would
	// have reached the generator as two colliding constant declarations --
	// exactly the defect spec §5.6 says must be rejected, never mangled.
	"enum_value_collision",

	// enum_constant_cross_field_collision is review finding F1 on PR #39:
	// validateEnumValueNames' own doc comment claimed "an enum field's
	// generated constants live under that field's own EnumGoType and are
	// never mixed with another field's" -- false. A generated constant's
	// name is EnumGoType + GoName, and EnumGoType is itself
	// entityGoName + goName(fieldName) (internal/parse/field.go): field
	// "state" with value "x_y" produces "Task"+"State"+"XY" = "TaskStateXY",
	// and field "state_x" with value "y" produces "Task"+"StateX"+"Y" =
	// the same "TaskStateXY". Concatenating two variable-length prefixes is
	// ambiguous, so two different fields' constants collide freely -- this
	// rendered zero diagnostics before schemaPackageDecls grew one
	// packageDecl per enum constant.
	"enum_constant_cross_field_collision",
}

// successFixtures are the .yaml files under testdata/ that both
// internal/parse and this package accept with zero diagnostics -- the
// passing counterpart every error-severity rule in goldenCases needs
// (CLAUDE.md: "a test that cannot fail on the bug it exists to catch is
// worse than no test"). "valid" is the shared, multi-entity counterpart
// exercising most rules at once.
var successFixtures = map[string]bool{
	"valid": true,

	// declared_indexes_valid is the success counterpart of the four index
	// fixtures above: two declared entries over the same filter pair in
	// different orders, both legal (they are different indexes), zero
	// diagnostics.
	"declared_indexes_valid": true,

	// nullable_set_null is the success counterpart of set_null_on_required:
	// an optional relation may null out when its target is deleted.
	"nullable_set_null": true,

	// enum_value_same_raw_across_fields is the success counterpart of
	// enum_value_collision and enum_constant_cross_field_collision: two
	// enum fields named "status" on two different entities ("article" and
	// "task"), each with the exact same raw values ([draft, published]),
	// must not be reported as colliding with each other. Their generated
	// constants don't collide because EnumGoType already carries the
	// entity's own name ("ArticleStatus" vs "TaskStatus" -- see
	// packageDecl's doc comment), not because of any per-field scoping in
	// the check itself; validatePackageNames compares every enum constant
	// against every other one in the whole schema.
	"enum_value_same_raw_across_fields": true,

	// filter_reserved_belongs_to_ok is the false-positive check for issue
	// #53's rule: a belongsTo relation named "limit" resolves to the field
	// Column "limit_id" (internal/parse/relation.go's
	// `Column: name.Value + "_id"`), not "limit", so filtering on it must
	// stay legal even though the reserved check now exists.
	"filter_reserved_belongs_to_ok": true,
}

// TestValidate_NoOrphanFixtures fails when a testdata/*.yaml file is
// classified neither as a diagnostic fixture nor as a success fixture.
// Copied from internal/parse/golden_test.go's TestParse_NoOrphanFixtures:
// adding a fixture and forgetting to register it should be a test failure,
// not silence.
func TestValidate_NoOrphanFixtures(t *testing.T) {
	inGolden := make(map[string]bool, len(goldenCases))
	for _, name := range goldenCases {
		inGolden[name] = true
	}

	paths, err := filepath.Glob(filepath.Join("testdata", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no fixtures found under testdata/: the glob is wrong, not the corpus")
	}

	for _, p := range paths {
		name := strings.TrimSuffix(filepath.Base(p), ".yaml")
		if !inGolden[name] && !successFixtures[name] {
			t.Errorf("fixture %s.yaml is in neither goldenCases nor successFixtures, so nothing runs it: "+
				"add it to goldenCases (and create %s.diag with -update), or to successFixtures if it parses "+
				"and validates cleanly", name, name)
		}
	}
}

func TestValidate_Golden(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			f := readFixture(t, name)

			schema, parseDiags := parse.Parse(f)
			if len(parseDiags) != 0 {
				t.Fatalf("fixture %s.yaml must parse cleanly (it tests Validate, not Parse); got parse diagnostics:\n%s",
					name, parseDiags.Render(f))
			}
			if schema == nil {
				t.Fatalf("fixture %s.yaml: parse.Parse returned a nil schema with no diagnostics", name)
			}

			diags := Validate(schema, f.Name)
			got := diags.Render(f)

			diagPath := filepath.Join("testdata", name+".diag")
			if *update {
				if err := os.WriteFile(diagPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(diagPath)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("rendered diagnostics mismatch for %s.\n got:\n%s\nwant:\n%s", name, got, string(want))
			}
		})
	}
}

// TestValidate_SuccessFixtures asserts that every fixture in successFixtures
// parses and validates with zero diagnostics -- the passing counterpart
// CLAUDE.md's testing section requires alongside every failure case: "a rule
// tested only by its failure does not prove it accepts what it should."
func TestValidate_SuccessFixtures(t *testing.T) {
	for name := range successFixtures {
		t.Run(name, func(t *testing.T) {
			f := readFixture(t, name)

			schema, parseDiags := parse.Parse(f)
			if len(parseDiags) != 0 {
				t.Fatalf("fixture %s.yaml must parse cleanly; got parse diagnostics:\n%s", name, parseDiags.Render(f))
			}
			if schema == nil {
				t.Fatalf("fixture %s.yaml: parse.Parse returned a nil schema with no diagnostics", name)
			}

			diags := Validate(schema, f.Name)
			if len(diags) != 0 {
				t.Errorf("fixture %s.yaml: want zero diagnostics, got:\n%s", name, diags.Render(f))
			}
		})
	}
}

func readFixture(t *testing.T, name string) source.File {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("testdata", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return source.File{Name: "lapigo.yaml", Src: src}
}
