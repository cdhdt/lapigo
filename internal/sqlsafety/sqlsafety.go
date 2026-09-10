// Package sqlsafety is a narrow, syntactic check for spec §11's third
// acceptance criterion: no generated SQL contains an interpolated identifier
// or value (issue #33). It parses one Go source file with go/ast and reports
// every place a query-shaped string was built by fmt.Sprintf or string
// concatenation, rather than being a Go string constant addressed with
// `$1`-style placeholders.
//
// What it is for: internal/gen renders every store file this project ships
// (spec's build order step 7, issue #28, is where the first query string
// lands); CheckSource is meant to run over that rendered output, unchanged
// as new templates land, rather than being rewritten per template.
//
// What it structurally cannot catch, stated so the guarantee is not
// oversold:
//
//   - A query assembled by repeated Write/WriteString calls into a
//     strings.Builder or bytes.Buffer across several statements. That
//     requires dataflow analysis across a function body; this package looks
//     at one expression at a time.
//   - A query built through an intermediate helper function whose
//     implementation this package is not also pointed at -- CheckSource sees
//     one file's syntax, not a call graph.
//   - Interpolation the literal text gives no reason to suspect: the
//     "looks like SQL" heuristic keys on a SELECT/INSERT INTO/UPDATE/DELETE
//     FROM keyword appearing in the literal portion of the expression. A
//     query fragment with none of those words (a bare WHERE clause handed to
//     a helper, say) is invisible to it.
//   - An aliased fmt import (`f "fmt"`); only the unaliased `fmt.Sprintf` /
//     `fmt.Sprint` / `fmt.Sprintln` selector is recognised.
//
// It is a compiler-grade net for the shapes named in issue #33, not a proof
// of the property for arbitrary Go.
package sqlsafety

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
)

// Finding is one place CheckSource judged a query-shaped string to have been
// built dynamically instead of held as a constant.
type Finding struct {
	Pos     token.Position
	Rule    string // "sprintf-sql", "concat-sql", or "verb-in-sql-literal"
	Snippet string // the literal SQL-looking text that triggered the rule
}

func (f Finding) String() string {
	return fmt.Sprintf("%s: %s: %s", f.Pos, f.Rule, f.Snippet)
}

// sqlKeywordRE matches a SQL statement keyword as a whole word, case
// insensitively. It is deliberately narrow -- four keywords, not a SQL
// grammar -- because the property being checked is "was this interpolated",
// not "is this valid SQL"; a broader keyword list would only widen the
// surface for a coincidental match in an unrelated string.
var sqlKeywordRE = regexp.MustCompile(`(?i)\b(SELECT|INSERT\s+INTO|UPDATE|DELETE\s+FROM)\b`)

// looksLikeSQL reports whether s contains a SQL statement keyword.
func looksLikeSQL(s string) bool { return sqlKeywordRE.MatchString(s) }

// printfVerbRE matches a fmt verb: optional flags/width/precision followed by
// a verb letter. It intentionally excludes a bare trailing '%' and a doubled
// '%%' (fmt's own escape), and excludes a percent sign followed by a
// non-verb character such as the quote that closes a SQL LIKE pattern
// (`'%'`) -- both would otherwise read as a "stashed verb" false positive on
// ordinary parameterised SQL. The flag class deliberately omits the space
// flag (fmt's "% d"): including it turned any "% <word starting with a verb
// letter>" -- "100%% complete" among them -- into a false match, and that
// flag is obscure enough in practice that losing it costs nothing this
// package is trying to catch.
var printfVerbRE = regexp.MustCompile(`%[-+#0-9.]*[vTtbcdoOqxXUeEfFgGsp]`)

// hasPrintfVerb reports whether s contains a fmt verb sequence.
func hasPrintfVerb(s string) bool { return printfVerbRE.MatchString(s) }

// CheckSource parses src as one Go file (filename is used only in
// diagnostics and parse errors) and returns every place it built a
// query-shaped string dynamically. A parse error is returned, not
// swallowed: a file this package cannot read must not be reported as "no
// findings" -- that reads as a clean bill of health for content nobody
// examined.
func CheckSource(filename string, src []byte) ([]Finding, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, fmt.Errorf("sqlsafety: parse %s: %w", filename, err)
	}

	v := &visitor{fset: fset}
	ast.Walk(v, file)
	return v.findings, nil
}

type visitor struct {
	fset     *token.FileSet
	findings []Finding
}

// Visit implements ast.Visitor. It special-cases addition chains so that a
// multi-operand concatenation is flattened and judged once as a whole,
// rather than being re-examined once per nested "+" node; every other node
// descends through the default ast.Walk behaviour (return v).
func (v *visitor) Visit(n ast.Node) ast.Visitor {
	switch expr := n.(type) {
	case *ast.BasicLit:
		v.checkLiteral(expr)
	case *ast.CallExpr:
		v.checkSprintf(expr)
	case *ast.BinaryExpr:
		if expr.Op == token.ADD {
			leaves := flattenAdd(expr)
			v.checkConcat(expr, leaves)
			// Walk each leaf independently rather than falling through to
			// ast.Walk's default descent into X/Y: the leaves are already
			// the full, non-"+" operand set, and letting the default
			// descent proceed as well would re-visit the intermediate "+"
			// nodes flattenAdd already consumed, flagging the same
			// concatenation more than once.
			for _, leaf := range leaves {
				ast.Walk(v, leaf)
			}
			return nil
		}
	}
	return v
}

// checkLiteral flags a bare string literal that looks like SQL and still
// carries a printf verb -- the shape of a query template stored in one place
// (a const, a struct field) and formatted somewhere else CheckSource is not
// looking, which the call-site-based rules below cannot see.
func (v *visitor) checkLiteral(lit *ast.BasicLit) {
	if lit.Kind != token.STRING {
		return
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	if looksLikeSQL(s) && hasPrintfVerb(s) {
		v.record(lit.Pos(), "verb-in-sql-literal", s)
	}
}

// checkSprintf flags fmt.Sprintf/Sprint/Sprintln whose first (format-string)
// argument is a literal that looks like SQL. Parameterised SQL never needs
// runtime formatting -- the placeholder is `$1`, not `%s` -- so any use of
// this family against a SQL-shaped literal is exactly the pattern issue #33
// exists to close, independent of what the substituted argument is.
func (v *visitor) checkSprintf(call *ast.CallExpr) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return
	}
	switch sel.Sel.Name {
	case "Sprintf", "Sprint", "Sprintln":
	default:
		return
	}
	if len(call.Args) == 0 {
		return
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return
	}
	if looksLikeSQL(s) {
		v.record(call.Pos(), "sprintf-sql", s)
	}
}

// checkConcat flags a "+" chain whose flattened literal text looks like SQL,
// provided the chain mixes at least one string literal with at least one
// non-literal operand. A chain built entirely of string literals is a
// compile-time constant in every way that matters here -- Go itself could
// fold it -- so it carries no more risk than writing it as one literal, and
// is the documented, intended way to split a long query across lines (see
// the package doc's negative control). A chain with no literal operand at
// all gives the keyword regex nothing to match against and is outside what
// this heuristic can judge.
func (v *visitor) checkConcat(expr *ast.BinaryExpr, leaves []ast.Expr) {
	var text strings.Builder
	anyLiteral := false
	allLiteral := true
	for _, leaf := range leaves {
		lit, ok := leaf.(*ast.BasicLit)
		if ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				text.WriteString(s)
				anyLiteral = true
				continue
			}
		}
		allLiteral = false
	}
	if !anyLiteral || allLiteral {
		return
	}
	if looksLikeSQL(text.String()) {
		v.record(expr.Pos(), "concat-sql", strings.TrimSpace(text.String()))
	}
}

func (v *visitor) record(pos token.Pos, rule, snippet string) {
	v.findings = append(v.findings, Finding{
		Pos:     v.fset.Position(pos),
		Rule:    rule,
		Snippet: snippet,
	})
}

// flattenAdd returns expr's operands in left-to-right order, descending
// through nested "+" nodes so that a > 2-operand chain (a + b + c parses as
// (a + b) + c) is reported as one chain rather than as two overlapping ones.
func flattenAdd(expr *ast.BinaryExpr) []ast.Expr {
	var leaves []ast.Expr
	var walk func(e ast.Expr)
	walk = func(e ast.Expr) {
		if b, ok := e.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			walk(b.X)
			walk(b.Y)
			return
		}
		leaves = append(leaves, e)
	}
	walk(expr)
	return leaves
}
