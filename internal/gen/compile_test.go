package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// compileModule is the one temp module every fixture's output is written
// under (see this file's doc comment). It carries a dot in its first
// segment for realism, same as gen_test.go's testModulePath; isStdlibImport
// does not depend on it (see that function's own doc comment).
const compileModule = "example.com/lapigogolden"

// TestGenerate_OutputCompiles is spec §8's compile tier, and it is not
// optional decoration here: it is the correction pass for the import set.
//
// The formatter runs with FormatOnly, so goimports neither adds nor removes
// an import (spec §5.2). Whatever plan.go computed is what ships. The Go
// compiler is what catches an error in that set, in both directions -- a
// missing import is "undefined", a surplus one is "imported and not used" --
// which is strictly more than goimports offered, since goimports reports
// neither. Without this test the reversal recorded in spec §13 would have
// removed a wrong safety net and put nothing in its place.
//
// Every fixture's output goes into one temp module, each under its own
// subdirectory so that the same package name in two fixtures does not
// collide, and the module is built once. Since every fixture shares that
// one module (compileModule), the modulePath Generate is given for fixture
// name is compileModule+"/"+name -- fixture full's hooks package, for
// instance, has to reach compileModule+"/full/internal/gen/model", not
// compileModule's own internal/gen/model, which belongs to a different
// fixture entirely.
//
// The module files are the repository's own, with only the module path
// changed. That pins the generated code to the same pgx the generator is
// tested against and keeps the build offline: every module it names is
// already in the cache, because this repository builds. Surplus requires are
// harmless to `go build`; a missing one would not be, which is why the whole
// file is copied rather than a hand-picked subset.
func TestGenerate_OutputCompiles(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the compile tier under -short: it shells out to `go build`")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("no go toolchain on PATH: %v", err)
	}

	dir := t.TempDir()
	writeTempModule(t, dir)

	for _, name := range goldenCases {
		// Each fixture's own subdirectory is also its own import root:
		// compileModule + "/" + name is what a hooks file in that
		// subdirectory needs to reach that same fixture's model package,
		// since every fixture shares one module (compileModule) but is
		// written under its own subdirectory to avoid a path collision (see
		// this test's own doc comment).
		files, err := Generate(loadFixture(t, name), compileModule+"/"+name)
		if err != nil {
			t.Fatalf("Generate %s: %v", name, err)
		}
		for p, src := range files {
			dst := filepath.Join(dir, name, filepath.FromSlash(p))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dst, src, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir
	// -mod=mod because the copied go.mod carries requires the generated
	// packages do not all use; GOPROXY=off because a build that reaches the
	// network is a build whose result depends on the network.
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the generated code does not compile: %v\n%s", err, out)
	}
}

// writeTempModule copies the repository's go.mod and go.sum into dir, with
// the module path replaced so that the generated packages are addressable
// under it.
func writeTempModule(t *testing.T, dir string) {
	t.Helper()

	const repoRoot = "../.."
	gomod, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(gomod), "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "module ") {
		t.Fatalf("the repository's go.mod does not open with a module line: %q", lines[0])
	}
	lines[0] = "module " + compileModule

	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	gosum, err := os.ReadFile(filepath.Join(repoRoot, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.sum"), gosum, 0o644); err != nil {
		t.Fatal(err)
	}
}
