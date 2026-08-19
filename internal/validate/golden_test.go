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
	"filter_json",
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
}

// successFixtures are the .yaml files under testdata/ that both
// internal/parse and this package accept with zero diagnostics -- the
// passing counterpart every error-severity rule in goldenCases needs
// (CLAUDE.md: "a test that cannot fail on the bug it exists to catch is
// worse than no test"). "valid" is the shared, multi-entity counterpart
// exercising most rules at once.
var successFixtures = map[string]bool{
	"valid": true,

	// sort_key_default_and_readonly_exempt is the mutation-gap regression
	// for two of validateSortKeyMutability's four exemptions: valid.yaml's
	// only default-carrying sort key ("created_at") is also `immutable:
	// true`, so `f.Immutable` alone already exempts it and a mutant deleting
	// `|| f.Default != nil` would go unnoticed there; valid.yaml's only
	// readonly field ("slug") is never a sort key at all, so `|| f.ReadOnly`
	// is exercised nowhere. This fixture puts a default-only key
	// ("created_at", no `immutable:`) and a readonly-only key ("slug", no
	// `immutable:`/`default:`) into the same sort spec, both non-last, so
	// either exemption's deletion produces a spurious mutability warning
	// where this fixture expects none.
	"sort_key_default_and_readonly_exempt": true,
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
