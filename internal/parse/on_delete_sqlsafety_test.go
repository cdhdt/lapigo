package parse

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strconv"
	"testing"
)

// This file backs the second trust assumption internal/ddl's sqlsafety
// check (issue #33) makes about code outside internal/ddl: ir.Relation's
// OnDelete field, written unquoted after "ON DELETE " in the emitted
// ALTER TABLE statement, can only ever hold one of a fixed, closed set of
// SQL keywords -- never arbitrary schema or request text -- because
// onDeleteKeywords (field.go) is the only place that populates it and is a
// map literal, not a pass-through of the YAML value.
//
// The check parses field.go's source and reads the map literal's values
// directly, rather than importing the map (unexported) or trusting the
// doc comment above it, so a value added or changed there without updating
// this closed set fails here instead of silently widening what a schema
// author's on_delete: keyword can inject into the migration.

const onDeleteKeywordsSourceFile = "field.go"

// onDeleteKeywordValues parses filename and returns the string values of the
// package-level map literal named mapName, plus how many map literals of
// that name it found (0 or more than 1 means this check verified nothing,
// or verified the wrong thing).
func onDeleteKeywordValues(filename string, src []byte, mapName string) (values []string, mapsFound int, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		return nil, 0, err
	}

	for _, d := range file.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name != mapName {
					continue
				}
				lit, ok := vs.Values[i].(*ast.CompositeLit)
				if !ok {
					continue
				}
				mapsFound++
				for _, elt := range lit.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					bl, ok := kv.Value.(*ast.BasicLit)
					if !ok || bl.Kind != token.STRING {
						// A value that is not itself a string literal (a
						// variable, a function call) is exactly the shape
						// this check exists to catch: it means OnDelete
						// could hold something other than a fixed keyword.
						return nil, mapsFound, nil
					}
					s, err := strconv.Unquote(bl.Value)
					if err != nil {
						return nil, mapsFound, err
					}
					values = append(values, s)
				}
			}
		}
	}
	return values, mapsFound, nil
}

// TestOnDeleteKeywords_ClosedToKnownSQLKeywords is the real check, run
// against this package's own current source.
func TestOnDeleteKeywords_ClosedToKnownSQLKeywords(t *testing.T) {
	src, err := os.ReadFile(onDeleteKeywordsSourceFile)
	if err != nil {
		t.Fatalf("reading %s: %v", onDeleteKeywordsSourceFile, err)
	}
	values, mapsFound, err := onDeleteKeywordValues(onDeleteKeywordsSourceFile, src, "onDeleteKeywords")
	if err != nil {
		t.Fatalf("parsing %s: %v", onDeleteKeywordsSourceFile, err)
	}
	if mapsFound != 1 {
		t.Fatalf("found %d onDeleteKeywords map literals in %s, want exactly 1: "+
			"this check verified nothing or verified the wrong declaration", mapsFound, onDeleteKeywordsSourceFile)
	}
	if len(values) == 0 {
		t.Fatalf("onDeleteKeywords in %s has no string-literal values: "+
			"either the map is empty or a value is computed rather than a literal, "+
			"which internal/ddl's trust in rel.OnDelete does not survive", onDeleteKeywordsSourceFile)
	}

	allowed := map[string]bool{"RESTRICT": true, "CASCADE": true, "SET NULL": true}
	var bad []string
	for _, v := range values {
		if !allowed[v] {
			bad = append(bad, v)
		}
	}
	if len(bad) != 0 {
		sort.Strings(bad)
		t.Errorf("onDeleteKeywords in %s has value(s) outside {RESTRICT, CASCADE, SET NULL}: %v -- "+
			"internal/ddl writes rel.OnDelete into the migration unquoted, trusting this set stays closed",
			onDeleteKeywordsSourceFile, bad)
	}
}

// TestOnDeleteKeywordValues_CatchesAWidenedMap is this check's own RED
// proof: a map literal carrying a value outside the trusted set must be
// reported.
func TestOnDeleteKeywordValues_CatchesAWidenedMap(t *testing.T) {
	const src = `package parse

var onDeleteKeywords = map[string]string{
	"restrict": "RESTRICT",
	"anything": "'; DROP TABLE users; --",
}
`
	values, mapsFound, err := onDeleteKeywordValues("bad.go", []byte(src), "onDeleteKeywords")
	if err != nil {
		t.Fatalf("onDeleteKeywordValues: %v", err)
	}
	if mapsFound != 1 {
		t.Fatalf("mapsFound = %d, want 1", mapsFound)
	}
	found := false
	for _, v := range values {
		if v == "'; DROP TABLE users; --" {
			found = true
		}
	}
	if !found {
		t.Fatalf("values = %v, want the injected value to be visible so the caller's allowlist check rejects it", values)
	}
}

// TestOnDeleteKeywordValues_CatchesANonLiteralValue proves that a map whose
// value is computed rather than written as a literal is reported as
// "verified nothing" (mapsFound stays 1 but values comes back empty),
// rather than silently passing because there was nothing to iterate.
func TestOnDeleteKeywordValues_CatchesANonLiteralValue(t *testing.T) {
	const src = `package parse

func sqlFor(s string) string { return s }

var onDeleteKeywords = map[string]string{
	"restrict": sqlFor("RESTRICT"),
}
`
	values, mapsFound, err := onDeleteKeywordValues("bad.go", []byte(src), "onDeleteKeywords")
	if err != nil {
		t.Fatalf("onDeleteKeywordValues: %v", err)
	}
	if mapsFound != 1 {
		t.Fatalf("mapsFound = %d, want 1", mapsFound)
	}
	if len(values) != 0 {
		t.Fatalf("values = %v, want none: a computed map value should not read back as an "+
			"empty, trivially-allowed set", values)
	}
}
