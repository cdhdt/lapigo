package parse

import "strings"

// goInitialisms are the identifier segments goName renders fully upper-case,
// rather than merely capitalizing their first letter, matching the
// convention the spec assumes when it gives "user_id and userId both yield
// UserID" as its example of a name collision (spec §5.6). Go's own style
// guide keeps a longer list (URL, API, HTTP, ...); lapigo's schema
// vocabulary only ever produces "id", so that is the only entry needed here.
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
