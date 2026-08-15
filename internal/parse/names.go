package parse

import (
	"regexp"
	"strings"

	"github.com/cdhdt/lapigo/internal/source"
)

// identifierPattern is the complete grammar an entity name, field name or
// explicit `table:` value must match. Anything outside it -- an empty
// string, a slash, a brace, a space, a semicolon, a NUL byte -- reaches
// either a generated http.ServeMux route pattern (Endpoint.Path is built
// directly from a table name) or a generated SQL identifier (a column or
// table name), and neither of those is a context where free text is safe
// (spec §3, CLAUDE.md "identifiers come from a whitelist").
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// requireIdentifier reports a positioned diagnostic and returns false when
// at.Value does not match identifierPattern. what names the kind of
// identifier in the message ("table name", "entity name", "field name").
func (r *resolver) requireIdentifier(at source.At[string], what string) bool {
	if identifierPattern.MatchString(at.Value) {
		return true
	}
	r.addAt(at,
		"identifiers must start with a letter or underscore, and contain only letters, digits, and underscores",
		"%s %q is not a valid identifier", what, at.Value)
	return false
}

// requireExportableName is requireIdentifier plus the check goName's own
// doc comment defers to "the validator": an identifier built entirely of
// underscores (e.g. "___") satisfies identifierPattern but produces an
// empty goName, an empty Go struct field or type name reaching the
// generated code with nothing to catch it downstream. Used for entity and
// field names, which both need a non-empty GoName; not used for table
// names, which never go through goName.
func (r *resolver) requireExportableName(at source.At[string], what string) bool {
	if !r.requireIdentifier(at, what) {
		return false
	}
	if goName(at.Value) != "" {
		return true
	}
	r.addAt(at,
		"add at least one letter or digit; an identifier of only underscores has no exportable Go name",
		"%s %q has no exportable Go name", what, at.Value)
	return false
}

// goInitialisms are the identifier segments goName renders fully upper-case,
// rather than merely capitalizing their first letter, matching the
// convention the spec assumes when it gives "user_id and userId both yield
// UserID" as its example of a name collision (spec §5.6).
//
// This vocabulary is schema *field names* a user wrote, not Go source
// identifiers -- Go's own style guide keeps a much longer initialism list
// (URL, API, HTTP, ...), but nothing here draws from it. A field literally
// named `url` still produces GoName "Url", not "URL": only "id" is
// special-cased, because it is the one initialism the spec's own collision
// example (`user_id`/`userId` -> `UserID`) depends on. Extending this list
// to cover more of Go's own conventions is a real option, not a bug, but it
// is a deliberate policy choice for a future revision, not something this
// doc comment should claim already happened.
// Collision *detection* against this convention is the validator's job, not
// the parser's -- this function only has to produce the same name the
// validator will later compare against.
var goInitialisms = map[string]string{
	"id": "ID",
}

// goName converts a schema identifier (a snake_case entity, field, or
// relation name) into an exported Go identifier: each underscore-delimited
// segment is capitalized, with the initialisms in goInitialisms rendered
// fully upper-case.
//
// This is a naming *convention*, not a validated identifier. Rejecting a
// name that does not survive export cleanly (a leading digit, a Go keyword,
// a collision with another exported name) is the validator's job (spec
// §5.6, step 3 of the pipeline) -- deliberately out of this package's scope,
// which resolves structure, not policy.
func goName(s string) string {
	if s == "" {
		return ""
	}
	segments := strings.Split(s, "_")
	var b strings.Builder
	for _, seg := range segments {
		if seg == "" {
			continue
		}
		if up, ok := goInitialisms[strings.ToLower(seg)]; ok {
			b.WriteString(up)
			continue
		}
		r := []rune(seg)
		b.WriteString(strings.ToUpper(string(r[0])))
		b.WriteString(string(r[1:]))
	}
	return b.String()
}

// defaultTableName is the table a schema entity maps to when it does not
// declare `table:` explicitly (spec §3.1, §9 open question 1): the entity
// name with an "s" appended. Naive and predictable by design -- no
// dictionary of irregular plurals -- so an author who disagrees writes
// `table:` explicitly rather than fighting a heuristic.
func defaultTableName(entityName string) string {
	return entityName + "s"
}
