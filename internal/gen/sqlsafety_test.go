package gen

import (
	"strings"
	"testing"

	"github.com/cdhdt/lapigo/internal/sqlsafety"
)

// TestGenerate_NoSQLInterpolation is spec §11's third acceptance criterion --
// "no generated SQL contains an interpolated identifier or value" -- run
// mechanically over every .go file Generate renders for the golden corpus
// (issue #33).
//
// It runs over goldenCases, the same fixture set TestGenerate_Golden and
// TestGenerate_OutputCompiles use, rather than a fixture of its own: build
// order step 7 (issue #28) is what adds the first template that renders a
// query string, and a store package it adds falls under this test the
// moment Generate returns it, with no second mechanism to remember to wire
// up.
//
// What this proves TODAY: nothing yet emits a query, so this test passes
// vacuously -- see the checked-file-count assertion below, which is what
// keeps that vacuous pass honest instead of silent. What it will prove once
// step 7 lands: every store file's query strings are Go constants
// addressed with `$1`-style placeholders, never built by fmt.Sprintf or
// concatenation with a non-constant operand -- see internal/sqlsafety's
// package doc for exactly what the underlying check does and does not
// catch (it is a syntactic net keyed on SQL keywords appearing in literal
// text, not a proof for arbitrary Go).
func TestGenerate_NoSQLInterpolation(t *testing.T) {
	for _, name := range goldenCases {
		t.Run(name, func(t *testing.T) {
			files, err := Generate(loadFixture(t, name), testModulePath)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			checked := 0
			for path, src := range files {
				if !strings.HasSuffix(path, ".go") {
					continue
				}
				checked++

				findings, err := sqlsafety.CheckSource(path, src)
				if err != nil {
					t.Fatalf("sqlsafety.CheckSource(%s): %v", path, err)
				}
				for _, f := range findings {
					t.Errorf("%s: interpolated SQL: %s", f, f.Snippet)
				}
			}

			// A test that silently checks zero files is worse than no test:
			// a glob or extension typo would pass every run. Generate is
			// documented to return internal/gen-rooted Go files only (see
			// gen.go), so this must be nonzero for every fixture the golden
			// corpus carries.
			if checked == 0 {
				t.Fatalf("no .go files were checked for fixture %s: Generate's output or this test's "+
					"file filter is broken", name)
			}
		})
	}
}
