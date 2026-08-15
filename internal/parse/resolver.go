package parse

import (
	"fmt"
	"math"

	"github.com/goccy/go-yaml/ast"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/source"
)

// resolver holds the state threaded through one Parse call: the file name
// every diagnostic is stamped with, and the accumulator every check appends
// to. Nothing here stops at the first problem (spec §4.4) -- every method
// on resolver keeps walking after recording a diagnostic, so one Parse call
// reports every structural problem in the file, not just the first.
type resolver struct {
	file  string
	diags diag.Diagnostics
}

// add appends an Error-severity diagnostic spanning [pos, end).
func (r *resolver) add(pos, end source.Pos, hint, format string, args ...any) {
	r.diags.Add(diag.Diagnostic{
		Severity:  diag.Error,
		File:      r.file,
		Pos:       pos,
		EndColumn: end.Column,
		Message:   fmt.Sprintf(format, args...),
		Hint:      hint,
	})
}

// addAt appends an Error-severity diagnostic spanning the range recorded on
// at -- the common case for a diagnostic blaming a key, an enum member, or
// any other leaf the resolver already captured as a source.At[string].
func (r *resolver) addAt(at source.At[string], hint, format string, args ...any) {
	r.add(at.Pos, at.End, hint, format, args...)
}

// addNode appends an Error-severity diagnostic spanning n's own token, for
// diagnostics that blame a whole node (a wrong type found where a mapping
// or sequence was expected) rather than a leaf value already reduced to a
// source.At[string].
func (r *resolver) addNode(n ast.Node, hint, format string, args ...any) {
	start, end := spanOf(n.GetToken())
	r.add(start, end, hint, format, args...)
}

// nodeTypeName renders n's ast.NodeType the way a diagnostic should name it
// -- lower-case, matching the YAML vocabulary a schema author wrote
// ("mapping", "sequence", "string"), not goccy's exported Go identifiers
// ("MappingType", "SequenceType").
func nodeTypeName(n ast.Node) string {
	if n == nil {
		return "nothing"
	}
	switch n.Type() {
	case ast.MappingType, ast.MappingValueType:
		return "a mapping"
	case ast.SequenceType:
		return "a sequence"
	case ast.StringType:
		return "a string"
	case ast.LiteralType:
		// A block scalar (`|` or `>`) decodes to a Go string too, but is not
		// an *ast.StringNode -- requireString rejects it, so nodeTypeName
		// must not call it "a string" too, or the diagnostic reads as
		// self-contradictory: "must be a string, found a string".
		return "a block scalar"
	case ast.IntegerType:
		return "an integer"
	case ast.FloatType:
		return "a float"
	case ast.BoolType:
		return "a boolean"
	case ast.NullType:
		return "null"
	case ast.AnchorType:
		// Named plainly rather than falling into the "unexpected type"
		// default: an anchor node (`&name value`) genuinely does wrap a
		// mapping, sequence, or scalar, so "must be a mapping, found a
		// value of an unexpected type" was actively wrong -- it is a
		// mapping. lapigo does not resolve YAML anchors and aliases across
		// a schema (there is no use for them in this format), so the
		// honest answer is to name the construct and let the "must be a
		// mapping" wrapper make clear it isn't accepted, rather than
		// silently unwrapping it and pretending anchors are supported.
		return "an anchor (not supported in a lapigo schema)"
	case ast.AliasType:
		return "an alias (not supported in a lapigo schema)"
	case ast.MergeKeyType:
		// A `<<:` merge key's own key node fails entries' string-key check
		// and is reported through this same path -- named explicitly so
		// that single diagnostic reads as "map key must be a string, found
		// a merge key (not supported in a lapigo schema)" instead of
		// cascading through the generic "unexpected type" fallback for the
		// key, then separately for the merged-in value, then again for
		// whatever consumed the result.
		return "a merge key (not supported in a lapigo schema)"
	default:
		return "a value of an unexpected type"
	}
}

// requireMapping asserts that n is a block or flow YAML mapping, the shape
// every object in the schema format (an entity, a field's options, ...)
// must take. On mismatch it records "expected a mapping, found <type>" at
// n's own position and returns ok=false; callers skip whatever they were
// about to build from n rather than guessing.
func (r *resolver) requireMapping(n ast.Node, context string) (*ast.MappingNode, bool) {
	if n == nil {
		return nil, false
	}
	if m, ok := n.(*ast.MappingNode); ok {
		return m, true
	}
	r.addNode(n, "", "%s must be a mapping, found %s", context, nodeTypeName(n))
	return nil, false
}

// requireSequence asserts that n is a YAML sequence -- the shape `sort:`,
// `filters:`, `endpoints:` and `values:` all take. On mismatch it records
// "expected a sequence, found <type>" and returns ok=false.
func (r *resolver) requireSequence(n ast.Node, context string) (*ast.SequenceNode, bool) {
	if n == nil {
		return nil, false
	}
	if s, ok := n.(*ast.SequenceNode); ok {
		return s, true
	}
	r.addNode(n, "", "%s must be a sequence, found %s", context, nodeTypeName(n))
	return nil, false
}

// requireString asserts that n is a YAML string scalar, the shape every
// identifier, enum member and keyword value in the schema format takes.
// On a match it returns n's own *ast.StringNode with ok=true; on a
// mismatch it records "must be a string, found <type>" at n's own
// position -- rather than guessing at one -- and returns (nil, false).
func (r *resolver) requireString(n ast.Node, context string) (*ast.StringNode, bool) {
	if n == nil {
		return nil, false
	}
	if s, ok := n.(*ast.StringNode); ok {
		return s, true
	}
	r.addNode(n, "", "%s must be a string, found %s", context, nodeTypeName(n))
	return nil, false
}

// requireBool asserts that n is a YAML boolean scalar -- the shape every
// `pk:`, `required:`, `unique:`, `readonly:`, `immutable:` and `version:`
// value takes.
func (r *resolver) requireBool(n ast.Node, context string) (*ast.BoolNode, bool) {
	if n == nil {
		return nil, false
	}
	if b, ok := n.(*ast.BoolNode); ok {
		return b, true
	}
	r.addNode(n, "", "%s must be a boolean (true or false), found %s", context, nodeTypeName(n))
	return nil, false
}

// requireInt asserts that n is a YAML integer scalar -- the shape `max:`
// takes.
func (r *resolver) requireInt(n ast.Node, context string) (*ast.IntegerNode, bool) {
	if n == nil {
		return nil, false
	}
	if i, ok := n.(*ast.IntegerNode); ok {
		return i, true
	}
	r.addNode(n, "", "%s must be an integer, found %s", context, nodeTypeName(n))
	return nil, false
}

// intNodeValue extracts an int from an *ast.IntegerNode, whose Value field
// is documented as "int64 or uint64" (ast.Integer's doc comment) -- goccy
// picks whichever fits the literal's sign, so a plain positive literal like
// `max: 200` comes back as uint64, not int64. Asserting straight to int64
// without checking, as a first version of this code did, panics on every
// unsigned literal (CLAUDE.md: no panic in library code); ok is false for
// any value wider than an int on the current platform, or of neither
// integer kind at all.
func intNodeValue(n *ast.IntegerNode) (int, bool) {
	switch v := n.Value.(type) {
	case int64:
		if v < math.MinInt || v > math.MaxInt {
			return 0, false
		}
		return int(v), true
	case uint64:
		if v > math.MaxInt {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

// mapEntry is one key/value pair of a mapping, with the key already reduced
// to a source.At[string] -- the shape every "known keys" walk in this
// package iterates over.
type mapEntry struct {
	Key   source.At[string]
	Value ast.Node
}

// entries returns m's key/value pairs in declaration order, with each key
// resolved to a source.At[string]. A key that is not a plain string scalar
// (a merge key, an explicit `? key` mapping key, a numeric key) is reported
// as its own diagnostic and omitted from the result -- the schema format
// has no use for a non-string key anywhere, so there is nothing a caller
// could do with one.
func (r *resolver) entries(m *ast.MappingNode) []mapEntry {
	out := make([]mapEntry, 0, len(m.Values))
	for _, mv := range m.Values {
		keyNode, ok := mv.Key.(*ast.StringNode)
		if !ok {
			r.addNode(mv.Key, "", "map key must be a string, found %s", nodeTypeName(mv.Key))
			continue
		}
		out = append(out, mapEntry{
			Key:   atOf(keyNode.Value, keyNode.GetToken()),
			Value: mv.Value,
		})
	}
	return out
}

// checkUnknownKeys reports a diagnostic for every entry in entries whose key
// is not in known, naming the likely intent when the key is a close typo of
// one that is (spec §4.1's "actionable fix" bar; edit-distance suggestion
// per the parser brief). Unknown keys are never silently ignored (a dropped
// `requird: true` is a user losing an afternoon) -- every one gets its own
// diagnostic, at its own position, and the walk continues past it.
func (r *resolver) checkUnknownKeys(entries []mapEntry, context string, known []string) {
	knownSet := make(map[string]bool, len(known))
	for _, k := range known {
		knownSet[k] = true
	}
	for _, e := range entries {
		if knownSet[e.Key.Value] {
			continue
		}
		hint := ""
		if s := suggest(e.Key.Value, known); s != "" {
			hint = fmt.Sprintf("did you mean `%s`?", s)
		}
		r.addAt(e.Key, hint, "unknown key %q in %s", e.Key.Value, context)
	}
}
