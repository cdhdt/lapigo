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
//
// tok may be nil, or carry a nil Position: errors.go's syntaxErrorDiagnostic
// already guards tok.Position before touching it, proof the field is known
// to be nullable, but atOf and addNode both pass node.GetToken() straight
// through with no guard of their own -- reachable if goccy ever hands back a
// node whose token has no position. spanOf returns the zero span for either
// case rather than panicking (CLAUDE.md: no panic in library code); a
// diagnostic built from it renders with no "line:col" segment at all (see
// diag.Diagnostic.Pos's doc comment), which is a degraded report, not a
// crash.
func spanOf(tok *token.Token) (start, end source.Pos) {
	if tok == nil || tok.Position == nil {
		return source.Pos{}, source.Pos{}
	}
	if tok.Type == token.ImplicitNullType {
		return implicitNullSpan(tok)
	}
	start = source.Pos{Line: tok.Position.Line, Column: tok.Position.Column}
	raw := strings.Trim(tok.Origin, " \t\n\r")
	// raw can itself contain embedded newlines: a double-quoted scalar may
	// be folded across two or more physical source lines, and Origin
	// carries that written form verbatim. The old implementation counted
	// every rune of raw, newlines included, while leaving end.Line
	// hardcoded to the start line -- a multi-line scalar's combined width
	// (both lines summed) landed entirely on its first line, producing an
	// end column far past that line's actual content (defect 12).
	// end.Line now advances by the number of embedded newlines, so it no
	// longer lies about how many lines the token actually spans; the
	// column contribution is measured only from raw's own first line (the
	// part actually written on tok.Position.Line), truncating at the first
	// newline rather than summing every line's width onto one column
	// figure.
	newlines := strings.Count(raw, "\n")
	if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
		raw = raw[:idx]
	}
	end = source.Pos{
		Line:   tok.Position.Line + newlines,
		Column: tok.Position.Column + utf8.RuneCountInString(raw),
	}
	return start, end
}

// implicitNullSpan returns the span for a token goccy fabricated for a
// mapping value that was never written at all -- `key:` with nothing after
// the colon -- distinguished from an explicit `key: null` by
// token.ImplicitNullType (verified against goccy v1.19.2: an explicit
// `null` or `~` gets token.NullType instead). The fabricated token's own
// Position is computed as if the literal text " null" had actually
// appeared right after the colon, which routinely lands past the real end
// of the source line (defect 13) -- there is nothing at that column to
// underline.
//
// The mapping's own `:` token -- tok.Prev, always real and always within
// the line -- stands in instead: the span collapses to the single point
// right after it, the last position that genuinely was written.
func implicitNullSpan(tok *token.Token) (start, end source.Pos) {
	prev := tok.Prev
	if prev == nil || prev.Position == nil {
		// No preceding token to fall back to (should not occur for any
		// mapping value in a real schema file, since every value is
		// preceded by its key's `:`) -- collapse to a single point at the
		// fabricated token's own start rather than trusting its width.
		if tok.Position == nil {
			return source.Pos{}, source.Pos{}
		}
		p := source.Pos{Line: tok.Position.Line, Column: tok.Position.Column}
		return p, p
	}
	p := source.Pos{
		Line:   prev.Position.Line,
		Column: prev.Position.Column + utf8.RuneCountInString(strings.TrimRight(prev.Origin, " \t\n\r")),
	}
	return p, p
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
