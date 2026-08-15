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
}

// successFixtures are the .yaml files under testdata/ that both
// internal/parse and this package accept with zero diagnostics -- the
// passing counterpart every error-severity rule in goldenCases needs
// (CLAUDE.md: "a test that cannot fail on the bug it exists to catch is
// worse than no test"). "valid" is the shared, multi-entity counterpart
// exercising most rules at once.
var successFixtures = map[string]bool{
	"valid": true,
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
