package ir

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"testing"
)

// This file backs one of the two trust assumptions internal/ddl's own
// sqlsafety check (issue #33) makes about code outside internal/ddl:
// (*Field).PgType() never returns anything built from arbitrary string
// data, so the DDL emitter is safe to write its result straight into a
// CREATE TABLE column-type position with no quoting of its own.
//
// "Safe" here means: every return statement in FieldType.PgType and
// Field.PgType is either a string literal, a fmt.Sprintf whose only verbs
// are %d (formatting an int -- Field.Max, never identifier or request
// text), or a call back into the other of the two (mutually verified by
// running this check against both files).

// pgTypeFinding is one return statement this check could not prove safe.
type pgTypeFinding struct {
	pos token.Position
}

// pgTypeFindings parses filename (Go source) and returns every return
// statement of a method named "PgType" that is not a string literal, a
// %d-only fmt.Sprintf, or a recursive PgType() call. It also reports how
// many PgType methods it found, so a caller can fail loudly if a rename
// left it checking nothing.
func pgTypeFindings(filename string, src []byte) (findings []pgTypeFinding, methodsFound int, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, 0, err
	}

	for _, d := range file.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Recv == nil || fd.Name.Name != "PgType" {
			continue
		}
		methodsFound++
		ast.Inspect(fd.Body, func(n ast.Node) bool {
			ret, ok := n.(*ast.ReturnStmt)
			if !ok {
				return true
			}
			for _, r := range ret.Results {
				if !isSafePgTypeExpr(r) {
					findings = append(findings, pgTypeFinding{fset.Position(r.Pos())})
				}
			}
			return true
		})
	}
	return findings, methodsFound, nil
}

func isSafePgTypeExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return e.Kind == token.STRING
	case *ast.CallExpr:
		sel, ok := e.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		if sel.Sel.Name == "PgType" && len(e.Args) == 0 {
			// Verified by the sibling call to pgTypeFindings against the
			// file that defines that method.
			return true
		}
		if sel.Sel.Name == "Sprintf" {
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "fmt" || len(e.Args) == 0 {
				return false
			}
			lit, ok := e.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return false
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return false
			}
			return onlyIntVerbs(s)
		}
	}
	return false
}

// verbRE matches a fmt verb (capturing its letter) or an escaped "%%"
// (captured as "%").
var verbRE = regexp.MustCompile(`%[-+#0-9.]*([a-zA-Z%])`)

// onlyIntVerbs reports whether every fmt verb in s is %d (or its own %%
// escape) -- never %s, %q, %v, or anything else that could carry
// unconstrained string data into a Postgres type/length position. A format
// string with no verb at all is also rejected: PgType has no reason to call
// Sprintf on a string with nothing to substitute.
func onlyIntVerbs(s string) bool {
	matches := verbRE.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return false
	}
	for _, m := range matches {
		if m[1] != "d" && m[1] != "%" {
			return false
		}
	}
	return true
}

// TestFieldType_PgType_NeverInterpolatesAString is the real check, run
// against this package's own current source.
func TestFieldType_PgType_NeverInterpolatesAString(t *testing.T) {
	for _, filename := range []string{"field_type.go", "field.go"} {
		src, err := os.ReadFile(filename)
		if err != nil {
			t.Fatalf("reading %s: %v", filename, err)
		}
		findings, methodsFound, err := pgTypeFindings(filename, src)
		if err != nil {
			t.Fatalf("parsing %s: %v", filename, err)
		}
		if methodsFound == 0 {
			t.Fatalf("%s defines no PgType method: this check verified nothing "+
				"(a rename or move would silently defang it)", filename)
		}
		for _, f := range findings {
			t.Errorf("%s: PgType return is not a literal, a %%d-only fmt.Sprintf, "+
				"or a nested PgType() call -- it could return arbitrary string data "+
				"into a CREATE TABLE column-type position", f.pos)
		}
	}
}

// TestPgTypeFindings_CatchesInterpolatedString is this check's own RED
// proof: a synthetic PgType method that formats a %s verb -- the shape a
// request-controlled or otherwise unconstrained string could ride into a
// column-type position through -- must be reported.
func TestPgTypeFindings_CatchesInterpolatedString(t *testing.T) {
	const src = `package ir

import "fmt"

type badType int

func (t badType) PgType() string {
	return fmt.Sprintf("%s", "whatever a future edit passes here")
}
`
	findings, methodsFound, err := pgTypeFindings("bad.go", []byte(src))
	if err != nil {
		t.Fatalf("pgTypeFindings: %v", err)
	}
	if methodsFound != 1 {
		t.Fatalf("methodsFound = %d, want 1", methodsFound)
	}
	if len(findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", findings)
	}
}

// TestPgTypeFindings_NegativeControl proves the same detector accepts the
// two shapes the real PgType methods actually use, so the positive result
// above is evidence of a working check and not an oversensitive one.
func TestPgTypeFindings_NegativeControl(t *testing.T) {
	const src = `package ir

import "fmt"

type goodType int

func (t goodType) PgType() string {
	if t == 1 {
		return fmt.Sprintf("varchar(%d)", 200)
	}
	return "text"
}
`
	findings, methodsFound, err := pgTypeFindings("good.go", []byte(src))
	if err != nil {
		t.Fatalf("pgTypeFindings: %v", err)
	}
	if methodsFound != 1 {
		t.Fatalf("methodsFound = %d, want 1", methodsFound)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none", findings)
	}
}
