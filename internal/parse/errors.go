package parse

import (
	yaml "github.com/goccy/go-yaml"

	"github.com/cdhdt/lapigo/internal/diag"
)

// syntaxErrorDiagnostic converts a *yaml.SyntaxError into the same
// Diagnostic type every check in this package produces (spec §4.4), using
// the offending token's own position rather than the multi-line,
// source-annotated text SyntaxError.Error() would otherwise produce --
// diag.Render builds that annotation itself, from the same source every
// other diagnostic in the report is rendered against, so letting goccy
// render its own copy would print the snippet twice in two different
// formats.
func syntaxErrorDiagnostic(file string, err *yaml.SyntaxError) diag.Diagnostic {
	tok := err.GetToken()
	d := diag.Diagnostic{
		Severity: diag.Error,
		File:     file,
		Message:  err.GetMessage(),
	}
	if tok != nil && tok.Position != nil {
		start, end := spanOf(tok)
		d.Pos = start
		d.EndColumn = end.Column
	}
	return d
}
