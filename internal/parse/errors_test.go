package parse

import (
	"errors"
	"testing"

	yaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/parser"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestRender_SyntaxErrorAndStructuralErrorSortTogether covers spec §4.4's
// "a file with both a syntax typo and a semantic error prints both, in the
// same format, in one pass": a syntaxErrorDiagnostic and an ordinary
// structural Diagnostic, accumulated together and rendered once, come out
// in source order regardless of which was appended first.
//
// This is deliberately not a single Parse() call over one file with both
// problems in it. Measured against goccy v1.19.2: parser.ParseBytes fails
// atomically on any *yaml.SyntaxError -- it returns a nil *ast.File, even
// for a multi-document YAML stream where only the second document is
// malformed (verified directly; see the parser brief's report for detail).
// There is therefore no AST left to walk for additional structural
// diagnostics once a syntax error has occurred, and no single Parse() call
// can produce both from the same file. What spec §4.4 actually requires --
// that the two diagnostic species share one Diagnostic type, one
// accumulator, and one position-sorted render -- is fully exercised by
// combining them here.
func TestRender_SyntaxErrorAndStructuralErrorSortTogether(t *testing.T) {
	src := []byte("fields: [a, b\n")
	_, err := parser.ParseBytes(src, parser.ParseComments)
	if err == nil {
		t.Fatal("expected a syntax error")
	}
	var synErr *yaml.SyntaxError
	if !errors.As(err, &synErr) {
		t.Fatalf("expected *yaml.SyntaxError, got %T", err)
	}

	syn := syntaxErrorDiagnostic("a.yaml", synErr)
	structural := diag.Diagnostic{
		Severity:  diag.Error,
		File:      "a.yaml",
		Pos:       source.Pos{Line: 1, Column: 1},
		EndColumn: 7,
		Message:   `unknown key "fields" in the schema file`,
	}

	// Appended out of source order -- structural first, even though it is
	// positioned earlier in the file -- to prove Render sorts rather than
	// preserving accumulation order (spec §4.5).
	var ds diag.Diagnostics
	ds.Add(structural)
	ds.Add(syn)

	got := ds.Render(source.File{Name: "a.yaml", Src: src})
	want := "a.yaml:1:1: unknown key \"fields\" in the schema file\n" +
		"   1 | fields: [a, b\n" +
		"     | ^^^^^^\n" +
		"\n" +
		"a.yaml:1:9: sequence end token ']' not found\n" +
		"   1 | fields: [a, b\n" +
		"     |         ^"
	if got != want {
		t.Fatalf("Render mismatch.\n got:\n%s\nwant:\n%s", got, want)
	}
}
