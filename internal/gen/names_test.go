package gen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"testing"

	"github.com/cdhdt/lapigo/internal/ir"
)

// declarationsIn parses every rendered file and returns, per Go package, the
// set of top-level declarations go/ast reports.
//
// "Declaration" is spec §5.6's definition, not a narrower one: types,
// functions, variables and constants, exported or not, AND methods. Methods
// are in the set on purpose -- internal/validate's reservedMethodNames is a
// list of METHOD names, so a table predicting only types would leave spec
// §10's debt exactly where it was. Unexported names are in for the opposite
// reason: a generated scanArticle row helper is a real declaration and its
// drift is real drift, and including it costs the collision check nothing,
// since a schema-derived name is always export-cased and can never equal
// one.
//
// A method is keyed "Receiver.Method", the receiver's type name with any
// pointer and any type parameters stripped. Keying it by the bare method
// name would make every entity's BeforeCreate the same declaration, which is
// the opposite of what the equality test needs to see.
func declarationsIn(t *testing.T, files map[string][]byte) map[string][]string {
	t.Helper()

	out := make(map[string][]string)
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	fset := token.NewFileSet()
	for _, p := range paths {
		f, err := parser.ParseFile(fset, p, files[p], parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("%s: the rendered output must parse as Go: %v", p, err)
		}
		pkg := f.Name.Name
		if want := path.Base(path.Dir(p)); pkg != want {
			t.Errorf("%s declares package %q but sits in directory %q", p, pkg, want)
		}
		for _, d := range f.Decls {
			out[pkg] = append(out[pkg], declKeys(t, p, d)...)
		}
	}
	return out
}

// declKeys returns the keys one top-level declaration contributes.
func declKeys(t *testing.T, file string, d ast.Decl) []string {
	t.Helper()

	switch d := d.(type) {
	case *ast.FuncDecl:
		if d.Recv == nil {
			return []string{d.Name.Name}
		}
		return []string{receiverTypeName(t, file, d.Recv) + "." + d.Name.Name}
	case *ast.GenDecl:
		var keys []string
		for _, spec := range d.Specs {
			switch spec := spec.(type) {
			case *ast.ImportSpec:
				// An import is not a declaration of this package's own.
			case *ast.TypeSpec:
				keys = append(keys, spec.Name.Name)
			case *ast.ValueSpec:
				for _, name := range spec.Names {
					keys = append(keys, name.Name)
				}
			default:
				t.Fatalf("%s: unhandled spec %T in a top-level declaration", file, spec)
			}
		}
		return keys
	default:
		t.Fatalf("%s: unhandled top-level declaration %T", file, d)
		return nil
	}
}

// receiverTypeName returns a method receiver's type name, stripped of a
// pointer and of type parameters, so that both `func (o Optional[T]) Get()`
// and `func (o *Optional[T]) Set()` key as "Optional".
func receiverTypeName(t *testing.T, file string, recv *ast.FieldList) string {
	t.Helper()

	if len(recv.List) != 1 {
		t.Fatalf("%s: a method receiver list has %d entries, want exactly 1", file, len(recv.List))
	}
	expr := recv.List[0].Type
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch e := expr.(type) {
	case *ast.IndexExpr:
		expr = e.X
	case *ast.IndexListExpr:
		expr = e.X
	}
	id, ok := expr.(*ast.Ident)
	if !ok {
		t.Fatalf("%s: unrecognised method receiver type %T", file, expr)
	}
	return id.Name
}

// TestDeclarationSet_MatchesRenderedOutput is spec §5.6's equality test and
// the closing of spec §10's step-3 debt.
//
// It asserts SET EQUALITY, per package, in BOTH directions: every top-level
// declaration in the rendered output is predicted by declarationSet, and
// every name declarationSet predicts is present in the output. Equality, not
// subset -- a subset in direction 1 alone passes when the table lists a name
// no template emits, and in direction 2 alone it passes when a template
// emits a name the table has never heard of, which is the drift this exists
// to catch. Only both together fail on both.
//
// The comparison is WITHIN a package, never across (spec §5.6): Article in
// model and ArticleStore in store are different packages and do not collide.
func TestDeclarationSet_MatchesRenderedOutput(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			schema := loadFixture(t, name)
			files, err := Generate(schema)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			got := declarationsIn(t, files)
			want := declarationSet(schema)

			for _, pkg := range unionOfKeys(got, want) {
				assertStringSetsEqual(t, "package "+pkg+"'s declarations", got[pkg], want[pkg])
			}
		})
	}
}

// TestPendingDeclarations_AreAbsentFromTheOutput is the other half of the
// same mechanism.
//
// declarationSet predicts what the templates emit today; pendingDeclarations
// carries spec §5.6's rows whose templates do not exist yet (steps 6 to 8).
// A pending name that turns up in the rendered output means someone added
// the template without moving the row, so the equality test above would fail
// with a name "nothing predicts" and no hint as to why. Failing here instead
// names the fix.
func TestPendingDeclarations_AreAbsentFromTheOutput(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			schema := loadFixture(t, name)
			files, err := Generate(schema)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			got := declarationsIn(t, files)
			pending := pendingDeclarations(schema)

			for pkg, names := range pending {
				emitted := make(map[string]bool, len(got[pkg]))
				for _, n := range got[pkg] {
					emitted[n] = true
				}
				for _, n := range names {
					if emitted[n] {
						t.Errorf("package %s emits %q, which is still listed as pending: "+
							"move that row from pendingDeclarations to declarationSet in names.go", pkg, n)
					}
				}
			}
		})
	}
}

// TestDeclarationMatrix_CoversEveryOptionInBothStates enforces spec §5.6's
// fixture-matrix rule: for every schema option that changes WHICH
// declarations are emitted, the matrix contains a fixture with it present
// and a fixture with it absent.
//
// A declaration emitted only for, say, entities that declare `update` is
// invisible to a matrix whose fixtures all declare it -- invisible in a way
// that looks exactly like a pass. Adding an option without extending the
// matrix must fail rather than quietly narrowing the coverage, which is what
// this test is for.
//
// `version:` is deliberately not here, and the omission is the point: it
// changes the BODIES of model and store declarations, not the set of them.
// The commit that first emits a version-conditional declaration adds the row
// and adds it here in the same change (spec §5.6).
func TestDeclarationMatrix_CoversEveryOptionInBothStates(t *testing.T) {
	options := map[string]func(*ir.Entity) bool{
		"endpoints: list":   (*ir.Entity).HasList,
		"endpoints: get":    (*ir.Entity).HasGet,
		"endpoints: create": (*ir.Entity).HasCreate,
		"endpoints: update": (*ir.Entity).HasUpdate,
		"endpoints: delete": (*ir.Entity).HasDelete,
		"an enum field":     func(e *ir.Entity) bool { return len(enumFields(e)) != 0 },
		"a belongsTo":       func(e *ir.Entity) bool { return len(e.Relations) != 0 },
	}

	present := make(map[string]bool, len(options))
	absent := make(map[string]bool, len(options))
	for _, name := range goldenCases {
		for _, e := range loadFixture(t, name).Entities {
			for opt, has := range options {
				if has(e) {
					present[opt] = true
				} else {
					absent[opt] = true
				}
			}
		}
	}

	names := make([]string, 0, len(options))
	for opt := range options {
		names = append(names, opt)
	}
	sort.Strings(names)
	for _, opt := range names {
		if !present[opt] {
			t.Errorf("no fixture in goldenCases has %q present: every declaration-changing "+
				"option needs a fixture in both states (spec §5.6)", opt)
		}
		if !absent[opt] {
			t.Errorf("no fixture in goldenCases has %q absent: every declaration-changing "+
				"option needs a fixture in both states (spec §5.6)", opt)
		}
	}
}

// unionOfKeys returns the sorted union of two maps' keys, so that a package
// present on one side only is still compared -- and therefore still fails.
func unionOfKeys(a, b map[string][]string) []string {
	seen := make(map[string]bool, len(a)+len(b))
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
