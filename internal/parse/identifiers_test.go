package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestTableIdentifier_RejectsUnvalidatedText is the regression test for
// defect 3: `table:` was accepted as free text and reached both an HTTP
// route pattern (Endpoint.Path) and a SQL identifier with zero validation.
// Every case here parsed with zero diagnostics before this test existed;
// each must now produce exactly one diagnostic naming the table itself.
func TestTableIdentifier_RejectsUnvalidatedText(t *testing.T) {
	cases := []struct {
		name  string
		table string
	}{
		{"empty", `""`},
		{"trailing_slash", `"x/"`},
		{"embedded_wildcard", `"x/{id}"`},
		{"wildcard_not_at_end", `"a/{x...}"`},
		{"sql_injection_shape", `"a b; DROP TABLE t --"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := "entities:\n  article:\n    table: " + c.table + "\n    fields:\n      id: { type: uuid, pk: true }\n"
			_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
			found := false
			for _, d := range diags {
				if d.Message == `table name `+quoteGo(c.table)+` is not a valid identifier` {
					found = true
				}
			}
			if !found {
				t.Fatalf("no 'table name ... is not a valid identifier' diagnostic among: %+v", diags)
			}
		})
	}
}

// quoteGo strips the YAML quoting c.table carries (it is written as a YAML
// double-quoted scalar so it round-trips through the fixture cleanly) down
// to the bare Go %q form the diagnostic message embeds.
func quoteGo(yamlQuoted string) string {
	// yamlQuoted is exactly `"..."`; the inner text is what YAML decodes to,
	// which for all cases above is byte-identical to the YAML source between
	// the quotes (no escapes used).
	return yamlQuoted
}

func TestTableIdentifier_NULByteInEntityNameRejected(t *testing.T) {
	src := "entities:\n  \"art\x00icle\":\n    fields:\n      id: { type: uuid, pk: true }\n"
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})
	found := false
	for _, d := range diags {
		if d.Message == `entity name "art\x00icle" is not a valid identifier` {
			found = true
		}
	}
	if !found {
		t.Fatalf("no entity-name identifier diagnostic among: %+v", diags)
	}
}

func TestFieldIdentifier_RejectsInvalidName(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      \"bad name\": { type: string }\n")
	want := `field name "bad name" is not a valid identifier`
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestFieldIdentifier_UnderscoreOnlyNameHasNoExportableGoName(t *testing.T) {
	msg := firstMessage(t, "entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n      ___: { type: string }\n")
	want := `field name "___" has no exportable Go name`
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}

func TestEntityIdentifier_UnderscoreOnlyNameHasNoExportableGoName(t *testing.T) {
	msg := firstMessage(t, "entities:\n  ___:\n    fields:\n      id: { type: uuid, pk: true }\n")
	want := `entity name "___" has no exportable Go name`
	if msg != want {
		t.Fatalf("Message = %q, want %q", msg, want)
	}
}
