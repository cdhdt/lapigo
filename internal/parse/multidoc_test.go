package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestParse_SecondDocumentIsRejected is the regression test for defect 10:
// Parse walked only astFile.Docs[0].Body and never looked at Docs[1:], so a
// second YAML document -- separated by a `---` -- was silently discarded
// even when it held an invalid schema of its own. A schema file must
// contain exactly one YAML document.
func TestParse_SecondDocumentIsRejected(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n" +
		"---\n" +
		"entities:\n  broken\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})

	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	found := false
	for _, d := range diags {
		if d.Message == "schema file has more than one YAML document; a schema file must contain exactly one" {
			found = true
			if d.Pos != (source.Pos{Line: 5, Column: 1}) {
				t.Errorf("Pos = %+v, want {5 1} (the second document's `---` separator)", d.Pos)
			}
		}
	}
	if !found {
		t.Fatalf("no 'more than one YAML document' diagnostic among: %+v", diags)
	}
}

// TestParse_SecondEmptyDocumentIsStillRejected guards the nil-Body edge
// case: a second document that is entirely empty (just a trailing `---`
// with nothing after it) must still be rejected, not silently ignored
// because there was "nothing there anyway" -- and must not panic
// dereferencing a nil Body.
func TestParse_SecondEmptyDocumentIsStillRejected(t *testing.T) {
	src := "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n" +
		"---\n"

	schema, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})

	if schema != nil {
		t.Fatalf("schema = %+v, want nil", schema)
	}
	found := false
	for _, d := range diags {
		if d.Message == "schema file has more than one YAML document; a schema file must contain exactly one" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no 'more than one YAML document' diagnostic among: %+v", diags)
	}
}
