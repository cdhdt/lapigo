package parse

import "github.com/goccy/go-yaml/ast"

// buildPendingFilters resolves a `filters:` sequence into the raw field
// names it names -- phase 1 supports equality only (spec §3.4), so there is
// no per-entry operator to parse, unlike a sort key's direction sign.
// Field resolution happens in the second pass, alongside sort keys and
// relations, for the same reason: a filter naming a belongsTo relation
// ("author") must resolve against the FK field buildRelationField appends,
// which is only guaranteed to exist once the whole fields: mapping has been
// walked.
func (r *resolver) buildPendingFilters(node ast.Node, entityName string) []pendingFilter {
	seq, ok := r.requireSequence(node, entityContext(entityName)+" `filters`")
	if !ok {
		return nil
	}
	out := make([]pendingFilter, 0, len(seq.Values))
	seen := make(map[string]bool, len(seq.Values))
	for _, v := range seq.Values {
		s, ok := r.requireString(v, entityContext(entityName)+" `filters` entry")
		if !ok {
			continue
		}
		if seen[s.Value] {
			r.addAt(atOf(s.Value, s.GetToken()), "each field may appear at most once in `filters`",
				"duplicate filter %q in %s", s.Value, entityContext(entityName))
			continue
		}
		seen[s.Value] = true
		out = append(out, pendingFilter{name: atOf(s.Value, s.GetToken())})
	}
	return out
}
