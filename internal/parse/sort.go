package parse

import (
	"strings"

	"github.com/goccy/go-yaml/ast"
)

// buildSortSpec resolves a `sort:` sequence into the single direction every
// key in it must share, and the raw (not yet field-resolved) keys carrying
// that direction's sign.
//
// Design decision, not delegated to a later validator: spec §3.3 rule 1
// ("one direction for the whole spec") is listed among the rules the parser
// brief assigns to validation, not parsing. But ir.SortSpec has exactly one
// Desc field for the whole spec (spec §2.2) -- there is nowhere in the IR to
// carry a per-key direction for a later validator to even inspect. By the
// time a SortSpec exists, "was every key's sign consistent" is no longer
// answerable from it. This function is therefore the last point that can
// check rule 1 at all, and does: the first key's sign fixes the spec's
// direction, and every later key with a different sign is reported here,
// with a hint pointing at the same 1.5 deferral the spec itself names.
func (r *resolver) buildSortSpec(node ast.Node, entityName string) (desc bool, keys []pendingSortKey) {
	seq, ok := r.requireSequence(node, entityContext(entityName)+" `sort`")
	if !ok {
		return false, nil
	}

	haveDirection := false
	seen := make(map[string]bool, len(seq.Values))
	for _, v := range seq.Values {
		s, ok := r.requireString(v, entityContext(entityName)+" `sort` entry")
		if !ok {
			continue
		}
		name, keyDesc := splitSortSign(s.Value)
		at := atOf(name, s.GetToken())
		if name == "" {
			r.addAt(atOf(s.Value, s.GetToken()), "name a field, e.g. `-created_at`", "sort key has no field name")
			continue
		}
		if !haveDirection {
			desc = keyDesc
			haveDirection = true
		} else if keyDesc != desc {
			r.addAt(atOf(s.Value, s.GetToken()),
				"mixed-direction sorts are deferred to phase 1.5; make every key's direction match the spec's",
				"sort key %q direction conflicts with the rest of the sort spec", s.Value)
			continue
		}
		if seen[name] {
			// A repeated key produces a keyset comparison like
			// "(id, id) < ($1, $2)" -- meaningless SQL, not merely
			// redundant.
			r.addAt(atOf(s.Value, s.GetToken()), "each field may appear at most once in `sort`",
				"duplicate sort key %q in %s", name, entityContext(entityName))
			continue
		}
		seen[name] = true
		keys = append(keys, pendingSortKey{name: at})
	}
	return desc, keys
}

// splitSortSign strips a leading '-' (descending) or '+' (ascending) sign
// from a sort key's written form, returning the bare field name and
// whether the key was written descending. A key with no sign is ascending,
// matching spec §3's example (`sort: [-created_at, -id]`, where the
// deferred-to-1.5 counter-example `[-created_at, id]` pairs an unsigned key
// with an ascending direction).
func splitSortSign(s string) (name string, desc bool) {
	switch {
	case strings.HasPrefix(s, "-"):
		return s[1:], true
	case strings.HasPrefix(s, "+"):
		return s[1:], false
	default:
		return s, false
	}
}
