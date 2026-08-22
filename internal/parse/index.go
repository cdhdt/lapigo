package parse

import (
	"github.com/goccy/go-yaml/ast"
)

// indexEntryKeys is the complete set of keys one `indexes:` item's mapping may
// declare. Exactly one key exists today: `filters`, naming the filter columns
// the index leads with (spec §3.5). checkUnknownKeys rejects everything else,
// so a typo (`filter:`, `columns:`) is a diagnostic rather than a silently
// ignored index.
var indexEntryKeys = []string{"filters"}

// buildPendingIndexes resolves an `indexes:` sequence into pendingIndex
// values, one per item, each carrying its `filters:` members as raw
// pendingFilter entries for the second pass (spec §3.5). The column names are
// not resolved to *ir.Field pointers here for the same reason `filters:` and
// `sort:` entries are not: a column may name a belongsTo FK field that a later
// `fields:` entry produces, and every not-found diagnostic flows through the
// same resolveSchema loop (see pendingSortKey's doc comment).
func (r *resolver) buildPendingIndexes(n ast.Node, entityName string) []pendingIndex {
	seq, ok := r.requireSequence(n, entityContext(entityName)+" `indexes`")
	if !ok {
		return nil
	}
	var out []pendingIndex
	for _, item := range seq.Values {
		m, ok := r.requireMapping(item, entityContext(entityName)+" `indexes` entry")
		if !ok {
			continue
		}
		entries := r.entries(m)
		r.checkUnknownKeys(entries, entityContext(entityName)+" `indexes` entry", indexEntryKeys)

		var filtersNode ast.Node
		for _, e := range entries {
			if e.Key.Value == "filters" {
				filtersNode = e.Value
			}
		}
		if filtersNode == nil {
			r.addNode(item, "add a `filters:` list naming the filters this index combines, e.g. `filters: [status, author]`",
				"`indexes` entry of entity %q has no `filters`", entityName)
			continue
		}
		fseq, ok := r.requireSequence(filtersNode, entityContext(entityName)+" `indexes` entry `filters`")
		if !ok {
			continue
		}
		if len(fseq.Values) == 0 {
			r.addNode(filtersNode, "name at least one filter column, or remove the `indexes` entry",
				"`indexes` entry `filters` of entity %q is empty", entityName)
			continue
		}
		columns := make([]pendingFilter, 0, len(fseq.Values))
		for _, v := range fseq.Values {
			s, ok := r.requireString(v, entityContext(entityName)+" `indexes` entry `filters` member")
			if !ok {
				continue
			}
			columns = append(columns, pendingFilter{name: atOf(s.Value, s.GetToken())})
		}
		out = append(out, pendingIndex{columns: columns})
	}
	return out
}
