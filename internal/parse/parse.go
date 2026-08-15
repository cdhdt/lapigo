// Package parse turns a lapigo.yaml schema file into a fully resolved
// *ir.Schema: YAML source, through goccy's positioned AST, into the IR every
// later phase 1 stage consumes (spec §2.1, §2.2).
//
// Templates never see raw YAML (CLAUDE.md's first architecture rule) and
// this package is the only place that ever imports goccy's ast and parser
// packages -- confining the AST walk to one package is deliberate (spec
// §2.3's recorded risk note on goccy's maintenance bus factor): a future
// replacement parser touches this package only.
package parse

import (
	"errors"

	yaml "github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// Parse turns f into a fully resolved *ir.Schema, or a set of diagnostics
// explaining why it could not.
//
// The pipeline (spec §2.1 step 2, build order §10.1-2):
//  1. Reject tabs outright (spec §4.2) -- goccy under-counts a column for
//     every tab in flow context, so any position computed after a tab has
//     been let through cannot be trusted, and CheckNoTabs is the only check
//     that runs before that trust is needed.
//  2. Parse with goccy, converting a *yaml.SyntaxError into the same
//     Diagnostic type every other check in this package produces.
//  3. Walk the AST and resolve it into an *ir.Schema, in two passes:
//     every *ir.Field first, then every pointer that must reference one of
//     them (spec §2.2's "sorting after resolving silently retargets a
//     pointer" hazard is why the order matters).
//  4. Call Schema.Freeze as a self-check. Any invariant it would catch that
//     a user's schema can actually trigger (no pk, two pk, a duplicate
//     field name, two fields marked `version: true`) is detected earlier,
//     with a position, as a normal diagnostic -- Freeze failing after that
//     point means this package has a bug, not the user's schema, and is
//     reported as such rather than panicking (CLAUDE.md: no panic in
//     library code).
func Parse(f source.File) (*ir.Schema, diag.Diagnostics) {
	if tabs := diag.CheckNoTabs(f.Name, f.Src); len(tabs) > 0 {
		return nil, tabs
	}

	astFile, err := parser.ParseBytes(stripBOM(f.Src), parser.ParseComments)
	if err != nil {
		var synErr *yaml.SyntaxError
		if errors.As(err, &synErr) {
			return nil, diag.Diagnostics{syntaxErrorDiagnostic(f.Name, synErr)}
		}
		return nil, diag.Diagnostics{{
			Severity: diag.Error,
			File:     f.Name,
			Message:  "internal: goccy returned an error parse could not classify: " + err.Error(),
		}}
	}

	if len(astFile.Docs) == 0 || astFile.Docs[0].Body == nil {
		return nil, diag.Diagnostics{{
			Severity: diag.Error,
			File:     f.Name,
			Message:  "schema file is empty; expected a top-level `entities:` mapping",
		}}
	}

	// A schema file must contain exactly one YAML document. Parse used to
	// walk only astFile.Docs[0].Body and never look at Docs[1:] -- so a
	// second document, separated by a `---`, was silently discarded even
	// when it held an invalid schema of its own (defect 10). Rejected here,
	// before any of the first document's AST is walked, rather than
	// resolved and then thrown away: a second document is a structural
	// mistake about the file itself, not a validation finding about a
	// schema.
	if len(astFile.Docs) > 1 {
		return nil, diag.Diagnostics{secondDocumentDiagnostic(f.Name, astFile.Docs[1])}
	}

	r := &resolver{file: f.Name}
	schema := r.resolveSchema(astFile.Docs[0].Body)
	if r.diags.HasErrors() {
		return nil, r.diags
	}

	return freezeOrReportBug(r, schema)
}

// freezeOrReportBug is Parse's step 4 (see Parse's doc comment): call
// Schema.Freeze as a self-check, once resolveSchema has reported zero
// diagnostics, and turn a failure into the same Diagnostic type every other
// check in this package produces rather than propagating a bare error or
// panicking (CLAUDE.md: no panic in library code).
//
// Split out from Parse deliberately, as its own function taking an
// already-built *ir.Schema: Parse's own signature (source.File in,
// *ir.Schema out) gives a test no way to inject a schema that is already
// resolved and already invalid, and every invariant Freeze checks is, by
// construction, unreachable through any schema this package's resolver can
// currently produce from valid user input once resolveSchema reports zero
// diagnostics -- resolvePK and resolveVersion preempt the two conditions a
// user's schema can actually trigger (no/two pk, two version fields), and
// every resolved pointer (Entity.PK, SortKey.Field, Filter.Field,
// Relation.Target) is drawn directly from the collection Freeze checks it
// belongs to, so it cannot fail to belong. Freeze failing here means this
// package itself has a bug that resolveSchema's diagnostics did not catch
// -- exactly the "not your schema file" case the message states -- and
// without this seam, that path, and the "delete the Freeze call" mutant it
// exists to kill, could not be exercised by any test of Parse.
func freezeOrReportBug(r *resolver, schema *ir.Schema) (*ir.Schema, diag.Diagnostics) {
	if err := schema.Freeze(); err != nil {
		r.diags.Add(diag.Diagnostic{
			Severity: diag.Error,
			File:     r.file,
			Message:  "internal: lapigo produced an invalid schema; this is a bug in lapigo, not your schema file: " + err.Error(),
		})
		return nil, r.diags
	}
	return schema, r.diags
}

// secondDocumentDiagnostic builds the "more than one YAML document"
// diagnostic, positioned at doc's own `---` separator when goccy recorded
// one (doc.Start), falling back to doc.Body's own token for a document
// missing an explicit separator token, and finally to no position at all
// for a doc that is both separator-less and bodyless (an empty trailing
// `---` with nothing after it) -- doc.GetToken() cannot be called blindly
// here, since *ast.DocumentNode.GetToken dereferences Body without a nil
// check.
func secondDocumentDiagnostic(file string, doc *ast.DocumentNode) diag.Diagnostic {
	d := diag.Diagnostic{
		Severity: diag.Error,
		File:     file,
		Message:  "schema file has more than one YAML document; a schema file must contain exactly one",
		Hint:     "remove everything from the second `---` onward, or split it into separate schema files",
	}
	switch {
	case doc.Start != nil:
		start, end := spanOf(doc.Start)
		d.Pos, d.EndColumn = start, end.Column
	case doc.Body != nil:
		start, end := spanOf(doc.Body.GetToken())
		d.Pos, d.EndColumn = start, end.Column
	}
	return d
}

// stripBOM removes a leading UTF-8 byte-order mark from src, if present.
// goccy's parser does not strip it itself -- a BOM left in place is folded
// into the first token's Value and Origin, which would shift every column
// on line 1 one rune to the right of where diag.CheckNoTabs and diag.Render
// (which both strip it) compute their own positions. Stripping it once,
// here, keeps all three in agreement.
func stripBOM(src []byte) []byte {
	const bom = "\xef\xbb\xbf"
	if len(src) >= len(bom) && string(src[:len(bom)]) == bom {
		return src[len(bom):]
	}
	return src
}
