package ddl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// This file is spec §11's third acceptance criterion (issue #33) applied to
// internal/ddl/ddl.go. The DDL emitter cannot parameterise its way out of
// interpolation the way store code will: `CREATE TABLE $1` is not valid SQL,
// a table or column name has to be inlined. So the checkable property here
// is not "nothing is inlined" -- it is "every inlined identifier or value
// went through the file's own escaping or derivation primitives"
// (quoteIdent, quoteSQL, fitName, (*emitter).indexName), rather than being
// written to the output buffer raw.
//
// The other half of spec §11's claim -- that what reaches those primitives
// is IR data, never anything else -- is not tested here because it does not
// need to be: Emit's exported entry point takes only *ir.Schema, nothing in
// this package reads a file, an environment variable, or a request, and
// every unexported function below it is reached only from that one
// parameter. There is no second source for a string to come from, so it is
// a fact about the package's signatures, not a runtime property a test
// could fail to observe. What a test *can* fail to observe -- and what this
// one checks -- is whether a value reaching the buffer was escaped or
// derived safely on the way there.
//
// The check parses ddl.go itself with go/ast rather than asserting on
// Emit's golden output, because the golden `.sql` files cannot distinguish
// "this identifier was quoted because it came from quoteIdent" from "this
// identifier happens to be surrounded by quote characters" -- both render
// identical bytes. Source-level analysis is what makes this checkable at
// all, and TestEmit_EveryDynamicWriteIsGuarded's own doc comment says so.

// ddlSourceFile is the file under test, read fresh on every run: this check
// is only meaningful against the real, current source, never a cached copy.
const ddlSourceFile = "ddl.go"

// safeWrapperFuncs are ddl.go's own escaping/derivation primitives. Calling
// one of them on arbitrary text is, by definition, the safe way to get that
// text into the output buffer:
//   - quoteIdent and quoteSQL escape (double- and single-quoting
//     respectively);
//   - fitName truncates and hash-suffixes a derived constraint/index name,
//     which is deliberately emitted unquoted -- ddl.go's own doc comment on
//     quoteIdent explains why: a derived name is built from identifier
//     segments plus a fixed suffix and is never a reserved word, so nothing
//     needs escaping.
//
// These four are the axioms this check trusts without recursing into their
// bodies (their bodies necessarily touch raw strings -- that is what makes
// them the escaping primitives, not something to flag). ddl_test.go pins
// their own behaviour separately (quoting rules, truncation length).
var safeWrapperFuncs = map[string]bool{
	"quoteIdent": true,
	"quoteSQL":   true,
	"fitName":    true,
}

// trustedExternalAccessors are calls or field reads into other packages'
// types that this file never escapes before writing, each safe for a reason
// this file cannot see and a sibling test below verifies instead of trusting
// as a comment:
//   - f.PgType() is checked by internal/ir's
//     TestFieldType_PgType_NeverInterpolatesAString: its implementation is a
//     closed switch over ir.FieldType returning fixed literals, with the
//     sole exception of a schema-authored int (never identifier or request
//     text) formatted into "varchar(N)".
//   - rel.OnDelete is checked by TestOnDeleteKeywords_ClosedToKnownSQLKeywords:
//     internal/parse populates it only from a fixed keyword map whose values
//     are drawn from {"RESTRICT", "CASCADE", "SET NULL"}.
var trustedExternalAccessors = map[string]bool{
	"PgType":   true, // call, e.g. f.PgType()
	"OnDelete": true, // field read, e.g. rel.OnDelete
}

// parseDDLFile parses ddlSourceFile and returns its function declarations
// keyed by name, for both the main check and its helper-function recursion.
func parseDDLFile(t *testing.T) (*token.FileSet, map[string]*ast.FuncDecl) {
	t.Helper()

	src, err := os.ReadFile(ddlSourceFile)
	if err != nil {
		t.Fatalf("reading %s: %v", ddlSourceFile, err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, ddlSourceFile, src, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", ddlSourceFile, err)
	}

	decls := make(map[string]*ast.FuncDecl)
	for _, d := range file.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			decls[fd.Name.Name] = fd
		}
	}
	return fset, decls
}

// guardChecker walks ddl.go's function bodies verifying that every string
// value reaching an output builder is safe by the rules above.
type guardChecker struct {
	fset  *token.FileSet
	decls map[string]*ast.FuncDecl
	// safeHelpers memoises the recursive verification of a helper function
	// defined in this file (e.g. defaultClause) so a function called from
	// several places is verified once.
	safeHelpers map[string]bool
	visiting    map[string]bool
}

// scope carries the two shapes of local data flow this file's functions use
// on the way to an output builder: `name := expr` short variable
// declarations, and `slice[i] = expr` element writes into a slice later
// joined (indexes' `quoted` column-name slice, joined by strings.Join before
// reaching group.WriteString).
type scope struct {
	assigns map[string]ast.Expr
	// elements maps a slice variable's name to every expression assigned
	// into one of its indices.
	elements map[string][]ast.Expr
}

// collectScope scans body for both `name := expr` declarations and
// `slice[i] = expr` element writes, including inside if/for statements
// (ddl.go's own `clause` is declared inside an `if` init, and `quoted` is
// filled inside a `for`). It does not model shadowing, reassignment, or
// control flow precisely -- a second write to the same name overwrites the
// first slot in these maps -- a known simplification, adequate for this
// single small file's current shape and re-checked on every run rather than
// assumed to stay adequate forever.
func collectScope(body *ast.BlockStmt) *scope {
	s := &scope{assigns: make(map[string]ast.Expr), elements: make(map[string][]ast.Expr)}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		switch lhs := as.Lhs[0].(type) {
		case *ast.Ident:
			if as.Tok == token.DEFINE {
				s.assigns[lhs.Name] = as.Rhs[0]
			}
		case *ast.IndexExpr:
			if as.Tok == token.ASSIGN {
				if id, ok := lhs.X.(*ast.Ident); ok {
					s.elements[id.Name] = append(s.elements[id.Name], as.Rhs[0])
				}
			}
		}
		return true
	})
	return s
}

// isSafeExpr reports whether expr, found inside a function whose local data
// flow is described by sc, is guaranteed to reach the output buffer only
// through a literal, a safe wrapper, a trusted external accessor, or
// (recursively) another helper function or local slice already proven safe.
func (c *guardChecker) isSafeExpr(expr ast.Expr, sc *scope) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		// STRING covers ordinary literals; CHAR covers the rune literals
		// passed to WriteByte (' ', '\n', the NUL separator). Both are
		// fixed at compile time.
		return e.Kind == token.STRING || e.Kind == token.CHAR

	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return false
		}
		return c.isSafeExpr(e.X, sc) && c.isSafeExpr(e.Y, sc)

	case *ast.ParenExpr:
		return c.isSafeExpr(e.X, sc)

	case *ast.Ident:
		rhs, ok := sc.assigns[e.Name]
		if !ok {
			return false
		}
		return c.isSafeExpr(rhs, sc)

	case *ast.CallExpr:
		switch fun := e.Fun.(type) {
		case *ast.Ident:
			if safeWrapperFuncs[fun.Name] {
				return true
			}
			return c.helperFuncIsSafe(fun.Name)
		case *ast.SelectorExpr:
			// em.indexName(...): a derived name built from fitName, safe by
			// the same reasoning as fitName itself.
			if fun.Sel.Name == "indexName" {
				return true
			}
			// group.String(): reads back a builder whose own WriteString
			// calls this same check already verified independently, when
			// they were visited as writes to "group".
			if x, ok := fun.X.(*ast.Ident); ok && x.Name == "group" && fun.Sel.Name == "String" {
				return true
			}
			// strings.Join(slice, sep): safe exactly when every element
			// ever written into slice is itself safe, and the separator is
			// a literal (indexes' `strings.Join(quoted, ", ")`, where
			// `quoted[i] = quoteIdent(n)` populated every element).
			if pkg, ok := fun.X.(*ast.Ident); ok && pkg.Name == "strings" && fun.Sel.Name == "Join" && len(e.Args) == 2 {
				return c.isSafeJoin(e.Args[0], e.Args[1], sc)
			}
			// A zero-argument call named in trustedExternalAccessors, e.g.
			// f.PgType() -- verified by a sibling test, not by recursing
			// into a package this file does not define.
			if len(e.Args) == 0 && trustedExternalAccessors[fun.Sel.Name] {
				return true
			}
			return false
		default:
			return false
		}

	case *ast.SelectorExpr:
		// A field read rather than a call, e.g. rel.OnDelete.
		return trustedExternalAccessors[e.Sel.Name]

	default:
		return false
	}
}

// isSafeJoin reports whether strings.Join(sliceArg, sepArg) is safe: sepArg
// must itself be safe, sliceArg must be a local variable this scope recorded
// at least one element write for, and every recorded element must be safe.
// A slice this check never saw written into (elements[name] absent, as
// opposed to present-but-empty) is not trusted -- absence means the slice's
// contents came from somewhere this check does not track, not that it is
// empty.
func (c *guardChecker) isSafeJoin(sliceArg, sepArg ast.Expr, sc *scope) bool {
	if !c.isSafeExpr(sepArg, sc) {
		return false
	}
	id, ok := sliceArg.(*ast.Ident)
	if !ok {
		return false
	}
	elems, ok := sc.elements[id.Name]
	if !ok || len(elems) == 0 {
		return false
	}
	for _, el := range elems {
		if !c.isSafeExpr(el, sc) {
			return false
		}
	}
	return true
}

// helperFuncIsSafe reports whether every return statement in the file-local
// function named name is, by isSafeExpr's rules, safe -- recursing so a
// helper that itself calls another helper is handled, and guarded against
// infinite recursion by visiting.
func (c *guardChecker) helperFuncIsSafe(name string) bool {
	if safe, done := c.safeHelpers[name]; done {
		return safe
	}
	if c.visiting[name] {
		// A cycle can't be proven safe by unwinding it; fail rather than
		// loop.
		return false
	}
	decl, ok := c.decls[name]
	if !ok || decl.Body == nil {
		return false
	}

	c.visiting[name] = true
	defer delete(c.visiting, name)

	sc := collectScope(decl.Body)
	safe := true
	foundReturn := false
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		foundReturn = true
		for _, r := range ret.Results {
			if !c.isSafeExpr(r, sc) {
				safe = false
			}
		}
		return true
	})
	// A helper with no return statement at all writes nothing a caller
	// could pass on as a string, so treating "no returns" as unsafe would
	// be vacuous in the wrong direction; but every helper this file
	// actually calls into from a WriteString argument does return a
	// string, so this arm is not expected to be exercised.
	if !foundReturn {
		safe = false
	}

	c.safeHelpers[name] = safe
	return safe
}

// isOutputBuilderReceiver reports whether x is the emitter's `em.b` field or
// the `group` local strings.Builder used in (*emitter).indexes -- the only
// two builders in this file whose contents end up in Emit's returned bytes.
// (columnListKey's own local `b` is excluded on purpose: it builds a map
// dedup key, never SQL output, so a raw column name reaching it is not the
// bug this check exists to catch.)
func isOutputBuilderReceiver(x ast.Expr) bool {
	if id, ok := x.(*ast.Ident); ok && id.Name == "group" {
		return true
	}
	if sel, ok := x.(*ast.SelectorExpr); ok {
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "em" && sel.Sel.Name == "b" {
			return true
		}
	}
	return false
}

// TestEmit_EveryDynamicWriteIsGuarded parses ddl.go and inspects every
// WriteString/WriteByte call against the two output builders, failing on
// any argument that is not a literal, a call to this file's own escaping or
// derivation primitives, a trusted external accessor, or a helper function
// this file can itself verify is built only from those.
//
// This test cannot carry its own permanently-red case the way a pure
// function's table test can: there is no "bad" ddl.go to keep on disk
// alongside the real one and still have `go build` succeed. The proof that
// it fails on the bug it exists to catch is instead a one-line mutation --
// unwrap a quoteIdent call, e.g. change `quoteIdent(e.Table)` on the
// CREATE TABLE line to plain `e.Table` -- run this test, observe the
// failure, then revert; the transcript is recorded in the PR description
// per issue #33's own instruction, not committed as a test.
func TestEmit_EveryDynamicWriteIsGuarded(t *testing.T) {
	fset, decls := parseDDLFile(t)
	c := &guardChecker{
		fset:        fset,
		decls:       decls,
		safeHelpers: make(map[string]bool),
		visiting:    make(map[string]bool),
	}

	checked := 0
	var unsafe []string
	for _, decl := range decls {
		if decl.Body == nil {
			continue
		}
		sc := collectScope(decl.Body)
		ast.Inspect(decl.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if sel.Sel.Name != "WriteString" && sel.Sel.Name != "WriteByte" {
				return true
			}
			if !isOutputBuilderReceiver(sel.X) {
				return true
			}
			if len(call.Args) != 1 {
				t.Fatalf("%s: %s.%s called with %d arguments, want 1",
					fset.Position(call.Pos()), exprString(sel.X), sel.Sel.Name, len(call.Args))
			}
			checked++
			if !c.isSafeExpr(call.Args[0], sc) {
				unsafe = append(unsafe, fset.Position(call.Args[0].Pos()).String()+": "+exprString(call.Args[0]))
			}
			return true
		})
	}

	// The negative control this test needs on itself: if a future edit to
	// ddl.go renamed the builders or stopped calling WriteString/WriteByte
	// on them, "checked" would silently drop to zero and every run above
	// would report a spotless zero findings -- the exact "test that cannot
	// fail" shape CLAUDE.md calls worse than no test. ddl.go currently
	// makes 52 such calls (45 on em.b, 7 on group); this asserts the count
	// stays in the neighbourhood rather than collapsing, so a check that
	// silently stopped checking anything is itself caught.
	const minExpectedCalls = 40
	if checked < minExpectedCalls {
		t.Fatalf("only inspected %d WriteString/WriteByte calls against em.b/group, want at least %d: "+
			"this check may have stopped matching ddl.go's output builders", checked, minExpectedCalls)
	}

	for _, u := range unsafe {
		t.Errorf("unguarded write to the SQL output buffer at %s (raw, not through quoteIdent/quoteSQL/fitName)", u)
	}
}

// exprString renders expr as it appears in ddl.go's own source, for
// diagnostics only.
func exprString(expr ast.Node) string {
	var b strings.Builder
	// A best-effort, printer-free rendering is enough for a test failure
	// message; go/printer would need the fset threaded through every call
	// site for no benefit here.
	ast.Inspect(expr, func(n ast.Node) bool {
		switch e := n.(type) {
		case *ast.Ident:
			b.WriteString(e.Name)
		case *ast.BasicLit:
			b.WriteString(e.Value)
		}
		return true
	})
	if b.Len() == 0 {
		return "<expr>"
	}
	return b.String()
}
