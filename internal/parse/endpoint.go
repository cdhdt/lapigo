package parse

import (
	"github.com/goccy/go-yaml/ast"

	"github.com/cdhdt/lapigo/internal/ir"
)

// endpointKeywords maps every YAML `endpoints:` keyword to its ir.EndpointKind.
var endpointKeywords = map[string]ir.EndpointKind{
	"list":   ir.EndpointList,
	"get":    ir.EndpointGet,
	"create": ir.EndpointCreate,
	"update": ir.EndpointUpdate,
	"delete": ir.EndpointDelete,
}

// endpointKeywordNames is endpointKeywords' key set, for edit-distance
// suggestions against an unrecognised entry.
var endpointKeywordNames = func() []string {
	out := make([]string, 0, len(endpointKeywords))
	for k := range endpointKeywords {
		out = append(out, k)
	}
	return out
}()

// defaultEndpointKinds is every CRUD operation, in the fixed order Path
// computation and rendering expect. Used when an entity omits `endpoints:`
// entirely.
//
// Design decision: the spec documents `endpoints: [list, get]` as an
// explicit *opt-out* of `create`/`update`/`delete` (spec §6.2, escape hatch
// 4) -- opting out only makes sense against a default of "generate
// everything". An entity that never writes `endpoints:` at all therefore
// gets the full CRUD set, least-astonishment for a schema author who has
// not thought about endpoint selection yet.
var defaultEndpointKinds = []ir.EndpointKind{
	ir.EndpointList, ir.EndpointGet, ir.EndpointCreate, ir.EndpointUpdate, ir.EndpointDelete,
}

// buildEndpoints resolves an `endpoints:` sequence into the declared
// Endpoints, in the order written -- or, when node is nil (the key was
// omitted), the full CRUD set in defaultEndpointKinds order.
//
// Design decision: an Endpoint's Path is not specified anywhere in spec §3
// (the schema format) -- it belongs to routing, a later generation phase.
// Since ir.Endpoint.Path is nonetheless part of the IR this package must
// produce, it is computed here from a convention the spec's own prose
// examples use elsewhere (§6.2's "GET /items/{id}"): the collection path
// (table name) for List/Create, and that path plus "/{id}" for
// Get/Update/Delete.
func (r *resolver) buildEndpoints(node ast.Node, table string) []ir.Endpoint {
	if node == nil {
		out := make([]ir.Endpoint, len(defaultEndpointKinds))
		for i, k := range defaultEndpointKinds {
			out[i] = ir.Endpoint{Kind: k, Path: endpointPath(k, table)}
		}
		return out
	}
	seq, ok := r.requireSequence(node, "`endpoints`")
	if !ok {
		return nil
	}
	out := make([]ir.Endpoint, 0, len(seq.Values))
	for _, v := range seq.Values {
		s, ok := r.requireString(v, "`endpoints` entry")
		if !ok {
			continue
		}
		k, ok := endpointKeywords[s.Value]
		if !ok {
			hint := ""
			if sug := suggest(s.Value, endpointKeywordNames); sug != "" {
				hint = "did you mean `" + sug + "`?"
			}
			r.addAt(atOf(s.Value, s.GetToken()), hint, "unknown endpoint %q", s.Value)
			continue
		}
		out = append(out, ir.Endpoint{Kind: k, Path: endpointPath(k, table)})
	}
	return out
}

// endpointPath computes the HTTP path for one endpoint kind on a table --
// see buildEndpoints' doc comment for why this convention lives here rather
// than being read from the schema.
func endpointPath(k ir.EndpointKind, table string) string {
	switch k {
	case ir.EndpointList, ir.EndpointCreate:
		return "/" + table
	default:
		return "/" + table + "/{id}"
	}
}
