package parse

import (
	"strings"
	"unicode/utf8"

	"github.com/goccy/go-yaml/token"

	"github.com/cdhdt/lapigo/internal/source"
)

// spanOf returns the start and exclusive end position of tok's written form,
// in the schema source.
//
// tok.Position.Column is a 1-based rune column (verified against goccy
// v1.19.2, not assumed): the start is exactly that. The end is computed from
// tok.Origin, which holds the token's source text as written but padded with
// the whitespace or newline that preceded it -- trimming that cutset and
// counting runes gives the written width, including any surrounding quotes
// a quoted scalar has. Recomputing the width from tok.Value instead would
// undercount a quoted key by the two quote characters, which is exactly the
// bug source.At's End field exists to avoid (see source.At's doc comment).
func spanOf(tok *token.Token) (start, end source.Pos) {
	start = source.Pos{Line: tok.Position.Line, Column: tok.Position.Column}
	raw := strings.Trim(tok.Origin, " \t\n\r")
	end = source.Pos{Line: tok.Position.Line, Column: tok.Position.Column + utf8.RuneCountInString(raw)}
	return start, end
}

// atOf pairs value with the span tok occupies in the source. value is taken
// as a separate argument, rather than tok.Value, because some callers need
// the At to carry a value that differs from the raw token text -- a sort key
// "-created_at" spans the whole written form including its direction sign,
// but the field it resolves to is looked up by the bare name "created_at".
func atOf(value string, tok *token.Token) source.At[string] {
	start, end := spanOf(tok)
	return source.NewAt(value, start, end)
}
