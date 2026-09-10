package parse

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// update regenerates every golden .diag file from the diagnostics Parse
// actually produces, rather than comparing against what is already on disk.
// `go test ./internal/parse/... -run TestParse_Golden -update` after a
// deliberate diagnostic wording change; the resulting diff to testdata/ is
// what gets reviewed, per CLAUDE.md's golden-file testing convention.
var update = flag.Bool("update", false, "update golden .diag files")

// goldenCases lists every table-driven diagnostic fixture: a .yaml file
// under testdata/ and the .diag file holding the exact text
// Diagnostics.Render is expected to produce for it. Success-path fixtures
// (parses with zero diagnostics) are asserted directly against the IR
// elsewhere (canonical_test.go, entity_test.go, ...); this table is for the
// diagnostic text itself, which spec §4 and CLAUDE.md both call a public
// contract to be asserted on exactly, never by substring.
var goldenCases = []string{
	"empty_file",
	"missing_entities_key",
	"entities_not_a_mapping",
	"entities_no_children",
	"entity_not_a_mapping",
	"entity_unknown_key",
	"entity_missing_fields",
	"fields_not_a_mapping",
	"field_not_a_mapping",
	"field_unknown_key",
	"field_missing_type",
	"field_unknown_type",
	"field_max_not_an_integer",
	"sort_not_a_sequence",
	"sort_empty_sequence",
	"sort_mixed_direction",
	"sort_unknown_field",
	"filters_unknown_field",
	"endpoints_unknown_value",
	"belongs_to_missing_target_key",
	"belongs_to_empty_target",
	"belongs_to_target_not_found",
	"no_pk",
	"two_pk",

	// version_wrong_type, version_not_required and version_is_pk are issue
	// #46's three rules for a `version: true` field, each in isolation:
	// wrong type (text, not int/bigint), nullable (no `required: true`),
	// and the field also being the entity's `pk: true`. version_is_pk is
	// the issue's own trap example, `id: { type: uuid, pk: true, version:
	// true }`, which fails two of the three rules at once (wrong type and
	// PK) -- its .diag pins both diagnostics, in the order the checks run.
	"version_wrong_type",
	"version_not_required",
	"version_is_pk",
	"duplicate_entity_name",
	"duplicate_field_name",
	"non_ascii_and_quoted_keys",
	"multiple_errors_sorted_by_position",
	"belongs_to_unknown_on_delete",
	"belongs_to_field_options",
	"indexes_unknown_key",
	"indexes_missing_filters",
	"indexes_unknown_field",
	"indexes_filters_empty",
	"enum_empty_value",
	"enum_control_char_value",

	// enum_unexportable_value pins the full rendered output of
	// buildEnumValues' empty-Go-identifier check (issue #24, review finding
	// F4 on PR #39): "---" is legal by enum_empty_value's and
	// enum_control_char_value's own rules (non-empty, no control character)
	// but every character is a separator, so goName has nothing left to
	// capitalize. TestBuildEnumValues_UnexportableValueIsDiagnostic
	// (coverage_test.go) already pins the Message text alone; this fixture
	// is this rule's sibling-style golden test, exact like its two
	// neighbours above -- a mutant that corrupts only the Hint (e.g. to
	// "BOGUS") survives the message-only unit test but fails this one.
	"enum_unexportable_value",
	"identifier_too_long",
}

// successFixtures are the .yaml files under testdata/ that parse cleanly and
// are asserted against the resulting IR elsewhere, so they carry no .diag.
//
// Every fixture must appear either here or in goldenCases; TestParse_NoOrphanFixtures
// enforces it. A hardcoded list that a new fixture can silently fall outside of
// is a fixture that never runs — which is exactly how belongs_to_unknown_on_delete
// sat untested with its error path uncovered.
var successFixtures = map[string]bool{
	"canonical": true,
	// declared_indexes carries the `indexes:` key of spec §3.5; its resolved
	// IR is asserted in index_test.go, including column pointer identity and
	// the exact spans of both declared columns.
	"declared_indexes": true,
}

// TestParse_NoOrphanFixtures fails when a testdata/*.yaml file is classified
// neither as a diagnostic fixture nor as a success fixture. Adding a fixture
// and forgetting to register it should be a test failure, not silence.
func TestParse_NoOrphanFixtures(t *testing.T) {
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
				"add it to goldenCases (and create %s.diag with -update), or to successFixtures if it parses cleanly "+
				"and is asserted against the IR elsewhere", name, name)
		}
	}
}

func TestParse_Golden(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			yamlPath := filepath.Join("testdata", name+".yaml")
			diagPath := filepath.Join("testdata", name+".diag")

			src, err := os.ReadFile(yamlPath)
			if err != nil {
				t.Fatal(err)
			}

			f := source.File{Name: "lapigo.yaml", Src: src}
			schema, diags := Parse(f)

			got := diags.Render(f)

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

			if len(diags) == 0 && schema == nil {
				t.Errorf("%s: no diagnostics but schema is nil", name)
			}
			if diags.HasErrors() && schema != nil {
				t.Errorf("%s: has error diagnostics but schema is non-nil", name)
			}
		})
	}
}
