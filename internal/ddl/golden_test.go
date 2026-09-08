package ddl

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/parse"
	"github.com/cdhdt/lapigo/internal/source"
	"github.com/cdhdt/lapigo/internal/validate"
)

// update regenerates every golden .sql file from what Emit actually
// produces, rather than comparing against what is already on disk.
// `go test ./internal/ddl/... -run TestEmit_Golden -update` after a
// deliberate output change; the resulting diff to testdata/ is what gets
// reviewed -- generated SQL is output this project's users will run against
// a real database, so a regeneration is never routine (CLAUDE.md's
// golden-file convention; internal/parse and internal/validate do the same).
var update = flag.Bool("update", false, "update golden .sql files")

// goldenCases lists every fixture whose rendered SQL is asserted byte for
// byte against a committed .sql file. Every fixture must parse cleanly and
// carry no error-severity validation diagnostics before Emit runs: Emit's
// contract is a frozen, validated schema, and a fixture that violates it
// would be testing a precondition the pipeline never produces.
var goldenCases = []string{
	// canonical is spec §3's own example schema, exercising in one file:
	// every phase 1 scalar's DDL type, varchar(max), enum CHECK, UNIQUE on a
	// nullable column, a belongsTo FK with explicit on_delete, `default:
	// now`, a version column, a written sort, and the N+1 derived index set.
	"canonical",

	// fk_cycle is two entities that belongsTo each other through nullable
	// columns -- the shape inline REFERENCES clauses cannot express in any
	// CREATE TABLE order, and the reason every FK is an ALTER TABLE.
	"fk_cycle",

	// self_reference is an entity whose FK targets its own table.
	"self_reference",

	// defaults pins the DEFAULT emission rules (spec §3.2): `now` becomes
	// CURRENT_TIMESTAMP, a literal renders as a quoted SQL string whatever
	// the column type (Postgres casts), and `uuid` emits no DEFAULT at all
	// because the generator mints uuids client-side.
	"defaults",

	// enum_quoting pins SQL string-literal escaping: an enum member
	// containing a single quote must double it, never break the CHECK
	// constraint out of its quotes.
	"enum_quoting",

	// index_name_collision has a filter column "a_b" and declared index
	// [a, b] over the same sort keys: two distinct column lists whose
	// joined names are identical, so the second CREATE INDEX name must
	// carry the disambiguating hash suffix.
	"index_name_collision",

	// long_names pushes table and column names past Postgres's 63-byte
	// identifier limit; every emitted name must come back within it and
	// remain distinct, via the deterministic truncation-plus-hash rule.
	"long_names",

	// index_dedup has a filter column that is also the first sort key, so
	// the per-filter derived index and the unfiltered sort index collapse
	// to one column list -- the emitted set must contain it once.
	"index_dedup",

	// reserved_identifiers has a table named "order" and columns "select",
	// "user" and "where": all legal per the parser's identifier grammar,
	// all SQL syntax errors unquoted. Every table and column position in
	// the output must carry double quotes.
	"reserved_identifiers",
}

func TestEmit_NoOrphanFixtures(t *testing.T) {
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
		if !inGolden[name] {
			t.Errorf("fixture %s.yaml is not in goldenCases, so nothing runs it: add it there "+
				"and create %s.sql with -update", name, name)
		}
	}
}

func TestEmit_Golden(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile(filepath.Join("testdata", name+".yaml"))
			if err != nil {
				t.Fatal(err)
			}
			f := source.File{Name: "lapigo.yaml", Src: src}

			schema, parseDiags := parse.Parse(f)
			if len(parseDiags) != 0 {
				t.Fatalf("fixture %s.yaml must parse cleanly (Emit's contract is a parsed schema); got:\n%s",
					name, parseDiags.Render(f))
			}

			diags := validate.Validate(schema, f.Name)
			if err := diags.Err(); err != nil {
				t.Fatalf("fixture %s.yaml must carry no error-severity diagnostics before Emit runs; got:\n%s",
					name, diags.Render(f))
			}

			got := string(Emit(schema))

			sqlPath := filepath.Join("testdata", name+".sql")
			if *update {
				if err := os.WriteFile(sqlPath, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(sqlPath)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("emitted SQL mismatch for %s.\n got:\n%s\nwant:\n%s", name, got, string(want))
			}

			// Postgres truncates identifiers at 63 bytes, silently; two of
			// ours colliding that way is a migration that fails to apply.
			// Checked here, on every fixture's real output, in addition to
			// the dedicated unit tests for the truncation rules.
			assertNoIdentifierExceedsLimit(t, got)
			assertNoDuplicateIdentifiers(t, got)
		})
	}
}

// identifierRunes is the character set of every identifier this package
// emits: schema identifiers validated by the parser's grammar plus the SQL
// keywords of the statements themselves.
func identifierByte(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

func assertNoIdentifierExceedsLimit(t *testing.T, sql string) {
	t.Helper()
	for i := 0; i < len(sql); {
		if !identifierByte(sql[i]) {
			i++
			continue
		}
		j := i
		for j < len(sql) && identifierByte(sql[j]) {
			j++
		}
		if n := j - i; n > maxIdentifierBytes {
			t.Errorf("identifier %q is %d bytes, over Postgres's %d-byte limit", sql[i:j], n, maxIdentifierBytes)
		}
		i = j
	}
}

// assertNoDuplicateIdentifiers extracts every CREATE INDEX name and every
// CONSTRAINT name and fails on a repeat: index names are database-scoped and
// constraint-created indexes share their namespace, so a duplicate would
// make the migration fail mid-apply. Names must come from the statement
// lines, not a whole-word scan -- a column name legitimately appears many
// times.
func assertNoDuplicateIdentifiers(t *testing.T, sql string) {
	t.Helper()
	seen := make(map[string]bool)
	for _, line := range strings.Split(sql, "\n") {
		var name string
		switch {
		case strings.HasPrefix(line, "CREATE INDEX "):
			name = strings.TrimPrefix(strings.SplitN(line, " ON ", 2)[0], "CREATE INDEX ")
		case strings.HasPrefix(line, "    CONSTRAINT "):
			name = strings.Fields(strings.TrimPrefix(line, "    CONSTRAINT "))[0]
		case strings.Contains(line, " ADD CONSTRAINT "):
			name = strings.Fields(strings.SplitN(line, " ADD CONSTRAINT ", 2)[1])[0]
		default:
			continue
		}
		if seen[name] {
			t.Errorf("identifier %q is declared twice in the emitted SQL", name)
		}
		seen[name] = true
	}
}
