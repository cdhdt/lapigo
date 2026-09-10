package ir

import "fmt"

// EndpointKind is which CRUD operation an Endpoint generates.
type EndpointKind int

const (
	EndpointList EndpointKind = iota
	EndpointGet
	EndpointCreate
	EndpointUpdate
	EndpointDelete
)

// String returns the YAML `endpoints:` keyword k was parsed from.
func (k EndpointKind) String() string {
	switch k {
	case EndpointList:
		return "list"
	case EndpointGet:
		return "get"
	case EndpointCreate:
		return "create"
	case EndpointUpdate:
		return "update"
	case EndpointDelete:
		return "delete"
	default:
		return fmt.Sprintf("EndpointKind(%d)", int(k))
	}
}

// Method returns the HTTP verb k's Endpoint is registered under, for the Go
// 1.22 `"METHOD /path"` routing pattern (spec §2.2, §6.6). List and Get are
// both GET; Update is PATCH, never PUT — spec §6.6 generates PATCH only, so
// there is no ambiguity here for Method to resolve.
//
// This does not touch Path: the route path convention is under separate
// debate (issue #23), and Method only ever needs to answer the verb.
func (k EndpointKind) Method() string {
	switch k {
	case EndpointList, EndpointGet:
		return "GET"
	case EndpointCreate:
		return "POST"
	case EndpointUpdate:
		return "PATCH"
	case EndpointDelete:
		return "DELETE"
	default:
		// Every known EndpointKind is listed explicitly above. An
		// out-of-range value (a bug in whoever constructed the Endpoint,
		// since the parser only ever produces the five known kinds) falls
		// through to this placeholder rather than silently registering a
		// route under a plausible-looking wrong verb.
		return fmt.Sprintf("<unknown EndpointKind %d>", int(k))
	}
}

// Endpoint is one generated HTTP operation on an Entity, selected by the
// `endpoints:` list (spec §3, §6.2 escape hatch 4). An entity that omits a
// kind from that list has no Endpoint for it, so, e.g., `create` is never
// generated.
type Endpoint struct {
	Kind EndpointKind
}

// Path returns the HTTP route path for kind on e (spec §2.2, §6.2's
// "GET /items/{id}" convention): the table name for the collection
// operations List and Create, and the table name plus a wildcard for the
// single-resource operations Get, Update and Delete.
//
// This is a computed method, not a stored field, for the same reason
// field.go documents for GoType/PgType: the only genuine inputs are Kind,
// e.Table and e.PK.Column, and a stored Path could be constructed
// inconsistently with them. There is a second, sharper reason here --
// buildEndpoints runs during entity resolution, but a belongsTo relation's
// PK.Column is only finalised in resolvePendingRelations' fixed point,
// so a Path computed and stored at that earlier point could freeze a
// placeholder. A method reads e.PK.Column at call time and cannot be wrong.
//
// The wildcard is named after e.PK.Column, never hardcoded to "{id}": an
// entity whose primary key is "slug" gets "/things/{slug}", so the
// generated handler's r.PathValue call names the column it actually binds
// to. This changes nothing on the wire -- a URL carries no wildcard names,
// and Go's ServeMux ignores them for conflict detection (spec §6.2).
func (e *Entity) Path(kind EndpointKind) string {
	switch kind {
	case EndpointList, EndpointCreate:
		return "/" + e.Table
	default:
		return "/" + e.Table + "/{" + e.PK.Column + "}"
	}
}

// HasList reports whether e's Endpoints include EndpointList.
func (e *Entity) HasList() bool { return e.hasEndpoint(EndpointList) }

// HasGet reports whether e's Endpoints include EndpointGet.
func (e *Entity) HasGet() bool { return e.hasEndpoint(EndpointGet) }

// HasCreate reports whether e's Endpoints include EndpointCreate. It gates
// whether e's CreateInput type, BeforeCreate/AfterCreate/AfterCreateCommitted
// hooks (spec §6.3) and the `POST` route are generated at all.
func (e *Entity) HasCreate() bool { return e.hasEndpoint(EndpointCreate) }

// HasUpdate reports whether e's Endpoints include EndpointUpdate. It gates
// whether e's UpdateInput type, the corresponding hooks (spec §6.3) and the
// `PATCH` route are generated at all.
func (e *Entity) HasUpdate() bool { return e.hasEndpoint(EndpointUpdate) }

// HasDelete reports whether e's Endpoints include EndpointDelete. It gates
// whether the corresponding hooks (spec §6.3) and the `DELETE` route are
// generated at all.
func (e *Entity) HasDelete() bool { return e.hasEndpoint(EndpointDelete) }

// hasEndpoint reports whether e.Endpoints contains kind. Endpoints is a
// small, unsorted slice (spec §2.2), so a linear scan is simpler than
// maintaining a parallel set that could drift out of sync with it.
func (e *Entity) hasEndpoint(kind EndpointKind) bool {
	for _, ep := range e.Endpoints {
		if ep.Kind == kind {
			return true
		}
	}
	return false
}
