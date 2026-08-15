package ir

// Schema is the fully resolved form of one lapigo.yaml file: every default
// expanded, every relation resolved to its target Entity, every sort key and
// filter resolved to its target Field. Templates consume a *Schema and
// nothing else — never YAML, never a parser AST (spec §2.2).
type Schema struct {
	// Entities is sorted by name. Anything the IR exposes for a template to
	// range over is a slice in this kind of defined order, never a map:
	// ranging a Go map is nondeterministic, and spec §5.3 requires
	// byte-identical output for the same input on every run.
	Entities []Entity
}

// Lookup returns the entity named name, or nil if the schema has none.
// Entities is small (one lapigo.yaml describes a handful of entities), so a
// linear scan over the sorted slice is simpler than maintaining a parallel
// map that could drift out of sync with it.
func (s *Schema) Lookup(name string) *Entity {
	for i := range s.Entities {
		if s.Entities[i].Name == name {
			return &s.Entities[i]
		}
	}
	return nil
}

// Entity is one resolved `entities:` member of a Schema (spec §2.2, §3).
//
// Entity has no Imports field. Imports are a property of an output file, not
// of an entity: a single entity's generation touches model, store, httpapi
// and hooks packages, each with a different import set, so one Imports field
// on Entity could not represent any of them correctly. The render step
// builds a per-file import set instead.
type Entity struct {
	Name      string // as written in lapigo.yaml
	GoName    string // validated Go identifier
	Table     string
	Fields    []Field // declaration order, not sorted — see Lookup
	PK        *Field
	Sort      SortSpec
	Filters   []Filter
	Relations []Relation
	Endpoints []Endpoint
}

// Lookup returns the field named name, or nil if the entity has none. It
// searches Fields in declaration order and returns a pointer into that
// slice, the same pointer identity that SortKey.Field, Filter.Field and
// Entity.PK hold for the field once the IR is fully resolved.
func (e *Entity) Lookup(name string) *Field {
	for i := range e.Fields {
		if e.Fields[i].Name.Value == name {
			return &e.Fields[i]
		}
	}
	return nil
}
