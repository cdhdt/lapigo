package gen

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// update regenerates every golden file from what Generate actually produces,
// rather than comparing against what is already on disk.
// `go test ./internal/gen/... -run Golden -count=1 -update`; the resulting
// diff to testdata/ is what gets reviewed. Generated Go is a contract --
// it is the code this project's users are handed -- so a regeneration is
// never routine (CLAUDE.md's golden-file convention; internal/ddl,
// internal/parse and internal/validate do the same).
var update = flag.Bool("update", false, "update golden files")

// goldenSuffix is appended to every golden file's own path. The rendered
// output is Go source, and a .go file under testdata/ would be picked up by
// `make fmt`'s goimports -w over the whole tree -- which would rewrite the
// goldens WITH import resolution enabled, the one thing spec §5.2 forbids,
// and leave the corpus silently disagreeing with the generator.
const goldenSuffix = ".golden"

// goldenCases lists every fixture whose rendered output is asserted byte for
// byte. It doubles as spec §5.6's declaration matrix -- see
// TestDeclarationMatrix_CoversEveryOptionInBothStates, which fails if these
// fixtures stop covering each declaration-changing option in both its
// present and its absent state.
var goldenCases = []string{
	// full has every declaration-changing option PRESENT -- all five
	// endpoints, an enum field, a belongsTo -- and, in one entity, every
	// scalar of spec §3.2 in both its nullable and non-nullable form. It is
	// therefore also where the Go type mapping and the computed import set
	// are pinned.
	"full",

	// list_only has `get`, `create`, `update` and `delete` absent, and no
	// enum field and no belongsTo: the absent state for six of the seven
	// options.
	"list_only",

	// no_list has `list` absent while the four other endpoints are present
	// -- the one state the two fixtures above do not reach between them.
	"no_list",
}

// TestGolden_NoOrphanFixtures fails on a testdata/*.yaml that no case runs.
// A fixture nothing renders is worse than no fixture: it looks like coverage
// in a directory listing and asserts nothing.
func TestGolden_NoOrphanFixtures(t *testing.T) {
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
				"and create its golden files with -update", name)
		}
	}
}

// TestGenerate_Golden compares Generate's whole output -- the set of paths
// and the bytes of every file -- against the committed corpus.
//
// The path set is compared in both directions, so a file that appears or
// disappears fails here rather than being absorbed by a per-file loop over
// whichever side happens to be iterated.
func TestGenerate_Golden(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			got, err := Generate(loadFixture(t, name), testModulePath)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			if *update {
				writeGolden(t, name, got)
			}

			want := readGolden(t, name)

			gotPaths := make([]string, 0, len(got))
			for p := range got {
				gotPaths = append(gotPaths, p)
			}
			wantPaths := make([]string, 0, len(want))
			for p := range want {
				wantPaths = append(wantPaths, p)
			}
			assertStringSetsEqual(t, "the golden corpus for "+name, gotPaths, wantPaths)

			sort.Strings(gotPaths)
			for _, p := range gotPaths {
				w, ok := want[p]
				if !ok {
					continue // already reported by the set comparison
				}
				if string(got[p]) != string(w) {
					t.Errorf("%s: rendered output differs from the golden file.\n got:\n%s\nwant:\n%s",
						p, got[p], w)
				}
			}
		})
	}
}

// goldenDir is the directory holding one fixture's golden files. The tree
// under it mirrors the output paths exactly, so a reviewer reading a diff
// sees the file the user would get, at the path they would get it.
func goldenDir(name string) string { return filepath.Join("testdata", name) }

// readGolden loads a fixture's golden corpus keyed by output path.
func readGolden(t *testing.T, name string) map[string][]byte {
	t.Helper()

	dir := goldenDir(name)
	out := make(map[string][]byte)
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, goldenSuffix) {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		src, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(strings.TrimSuffix(rel, goldenSuffix))] = src
		return nil
	})
	if err != nil {
		t.Fatalf("reading the golden corpus for %s: %v (run with -update to create it)", name, err)
	}
	if len(out) == 0 {
		t.Fatalf("the golden corpus for %s is empty: run with -update to create it", name)
	}
	return out
}

// writeGolden replaces a fixture's golden corpus with files. The directory is
// removed first, so a file the generator no longer produces disappears from
// the corpus instead of lingering as a golden nothing compares against.
func writeGolden(t *testing.T, name string, files map[string][]byte) {
	t.Helper()

	dir := goldenDir(name)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	for p, src := range files {
		dst := filepath.Join(dir, filepath.FromSlash(p)+goldenSuffix)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, src, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
