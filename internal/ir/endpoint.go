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

// Endpoint is one generated HTTP operation on an Entity, selected by the
// `endpoints:` list (spec §3, §6.2 escape hatch 4). An entity that omits a
// kind from that list has no Endpoint for it, so, e.g., `create` is never
// generated.
type Endpoint struct {
	Kind EndpointKind
	Path string
}
