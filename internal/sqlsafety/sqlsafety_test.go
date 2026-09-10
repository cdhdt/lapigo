package sqlsafety

import (
	"testing"
)

// TestCheckSource_Positive proves the detector actually fires. Each case is a
// minimal, compilable Go source file with one deliberately unsafe SQL
// construction -- an interpolated identifier or value reaching a query
// string through fmt.Sprintf, string concatenation, or a stashed printf verb.
//
// This is the RED half of the check: a detector nobody has watched refuse a
// known-bad input is not evidence of anything (issue #33).
func TestCheckSource_Positive(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantRule string
	}{
		{
			name: "fmt.Sprintf interpolates a table name into a SELECT",
			src: `package x

import "fmt"

func query(table string) string {
	return fmt.Sprintf("SELECT * FROM %s WHERE id = $1", table)
}
`,
			wantRule: "sprintf-sql",
		},
		{
			name: "fmt.Sprintf interpolates a value into an UPDATE",
			src: `package x

import "fmt"

func query(name string) string {
	return fmt.Sprintf("UPDATE users SET name = %s WHERE id = $1", name)
}
`,
			wantRule: "sprintf-sql",
		},
		{
			name: "string concatenation interpolates a table name",
			src: `package x

func query(table string) string {
	return "SELECT * FROM " + table
}
`,
			wantRule: "concat-sql",
		},
		{
			name: "string concatenation interpolates a column name into a WHERE clause",
			src: `package x

func query(col string) string {
	return "DELETE FROM users WHERE " + col + " = $1"
}
`,
			wantRule: "concat-sql",
		},
		{
			name: "a bare SQL literal carries a stashed printf verb",
			src: `package x

const q = "SELECT * FROM users WHERE id = %s"
`,
			wantRule: "verb-in-sql-literal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := CheckSource("x.go", []byte(tt.src))
			if err != nil {
				t.Fatalf("CheckSource: %v", err)
			}
			if len(findings) == 0 {
				t.Fatalf("CheckSource found nothing; want at least one %q finding -- "+
					"the detector did not fire on a known-bad input", tt.wantRule)
			}
			found := false
			for _, f := range findings {
				if f.Rule == tt.wantRule {
					found = true
				}
			}
			if !found {
				t.Errorf("CheckSource findings = %+v; want a finding with Rule %q", findings, tt.wantRule)
			}
		})
	}
}

// TestCheckSource_NegativeControls proves the detector does not fire on the
// shapes generated code is expected to use, or on ordinary Go string
// handling that has nothing to do with SQL. A check that flags everything
// is exactly as useless as one that flags nothing; both directions need
// evidence (issue #33's "verify the negative control").
func TestCheckSource_NegativeControls(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			name: "a parameterised query as a single Go constant",
			src: `package x

const listQuery = "SELECT id, name FROM users WHERE id > $1 ORDER BY id LIMIT $2"
`,
		},
		{
			name: "a parameterised query split across lines by pure literal concatenation",
			src: `package x

const listQuery = "SELECT id, name FROM users " +
	"WHERE id > $1 " +
	"ORDER BY id LIMIT $2"
`,
		},
		{
			name: "ordinary, non-SQL string concatenation",
			src: `package x

func greet(name string) string {
	return "hello, " + name
}
`,
		},
		{
			name: "fmt.Sprintf used for a non-SQL message",
			src: `package x

import "fmt"

func notFound(id int) string {
	return fmt.Sprintf("user %d not found", id)
}
`,
		},
		{
			name: "a SQL LIKE pattern's literal percent signs are not printf verbs",
			src: `package x

const search = "SELECT * FROM users WHERE name LIKE '%' || $1 || '%'"
`,
		},
		{
			name: "a doubled percent is fmt's own escape, not a verb",
			src: `package x

const q = "SELECT 100%% complete"
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			findings, err := CheckSource("x.go", []byte(tt.src))
			if err != nil {
				t.Fatalf("CheckSource: %v", err)
			}
			if len(findings) != 0 {
				t.Errorf("CheckSource findings = %+v; want none", findings)
			}
		})
	}
}

// TestHasPrintfVerb_MatchesKnownVerbs is the positive half of the regex's own
// negative control: before trusting that it matches nothing malicious, prove
// it matches something it must.
func TestHasPrintfVerb_MatchesKnownVerbs(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"%s", true},
		{"%d", true},
		{"%v", true},
		{"a %s b", true},
		{"%%", false},   // fmt's escaped percent
		{"100%", false}, // trailing percent, no verb letter follows
		{"'%'", false},  // SQL LIKE wildcard quoted as a literal, not a verb
		{"no percent here", false},
	}
	for _, tt := range tests {
		if got := hasPrintfVerb(tt.s); got != tt.want {
			t.Errorf("hasPrintfVerb(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

// TestCheckSource_ParseError reports a malformed file as an error rather than
// silently finding nothing -- a syntax error must not read as "no findings".
func TestCheckSource_ParseError(t *testing.T) {
	_, err := CheckSource("x.go", []byte("this is not valid go source {"))
	if err == nil {
		t.Fatal("CheckSource returned a nil error for unparseable source")
	}
}
