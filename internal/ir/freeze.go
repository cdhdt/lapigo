package ir

import "fmt"

// Freeze asserts every invariant a resolved Schema must hold that no Go
// compiler check can express: every resolved pointer is genuinely an element
// of the collection it claims to belong to, PK and Version bookkeeping do
// not contradict themselves, field names do not collide within an entity,
// and Entities is sorted by name (spec §2.2, §3.1). Call it once, after
// resolution; the rest of the pipeline is entitled to assume a frozen
// Schema is internally consistent.
//
// Comparisons throughout are by pointer identity, never by name. A decoy
// Field built during resolution — or a bug that copied a Field instead of
// keeping its address — can share a Name with the real one; identity is the
// only check that catches a resolved pointer naming something other than
// what it appears to.
//
// Freeze reports the first violation it finds rather than accumulating
// every one. It exists to assert a precondition, not to report every schema
// mistake to a user — that job belongs to the validator (spec §4), which
// runs earlier, on the AST, with positioned diagnostics.
func (s *Schema) Freeze() error {
	if s == nil {
		return fmt.Errorf("ir: Freeze called on a nil Schema")
	}
	for i, e := range s.Entities {
		if i > 0 && s.Entities[i-1].Name >= e.Name {
			return fmt.Errorf("ir: Schema.Entities is not sorted by name: %q at index %d does not precede %q",
				s.Entities[i-1].Name, i-1, e.Name)
		}
		if err := e.freeze(s); err != nil {
			return err
		}
	}
	return nil
}

// freeze checks the invariants scoped to a single entity.
func (e *Entity) freeze(s *Schema) error {
	seen := make(map[string]bool, len(e.Fields))
	var pk *Field
	pkCount, versionCount := 0, 0
	for _, f := range e.Fields {
		if seen[f.Name.Value] {
			return fmt.Errorf("ir: entity %q has more than one field named %q", e.Name, f.Name.Value)
		}
		seen[f.Name.Value] = true
		if f.PK {
			pkCount++
			pk = f
		}
		if f.Version {
			versionCount++
		}
		// Every element of Field.EnumValues is meant to carry a non-empty
		// GoName by construction (internal/parse/field.go's buildEnumValues
		// rejects any value whose computed identifier is empty before it is
		// ever appended) -- this is that invariant checked, not merely
		// asserted in EnumValue's doc comment. validateEnumValueNames
		// (internal/validate/names.go) compares EnumValue.GoName values
		// against each other to find collisions; an empty GoName reaching
		// that comparison from a hand-built or buggily-resolved Schema would
		// make two otherwise-unrelated enum values collide with each other
		// silently, on "", rather than each independently failing to have a
		// name at all.
		for _, v := range f.EnumValues {
			if v.GoName == "" {
				return fmt.Errorf("ir: entity %q field %q has an enum value %q with no exportable Go name", e.Name, f.Name.Value, v.Name.Value)
			}
		}
	}
	if pkCount != 1 {
		return fmt.Errorf("ir: entity %q has %d fields marked PK, want exactly 1 (spec §3.1)", e.Name, pkCount)
	}
	if versionCount > 1 {
		return fmt.Errorf("ir: entity %q has %d fields marked Version, want at most 1 (spec §3.1)", e.Name, versionCount)
	}
	// e.PK must be the very field that claims PK: true, not merely a field
	// with a matching name — the identity check defect 1 exists for. Since
	// pk was found by scanning e.Fields, this comparison also establishes
	// that e.PK is an element of e.Fields.
	if e.PK != pk {
		return fmt.Errorf("ir: entity %q PK does not point at the field marked PK", e.Name)
	}

	for _, k := range e.Sort.Keys {
		if !fieldBelongsTo(k.Field, e.Fields) {
			return fmt.Errorf("ir: entity %q has a sort key field that is not an element of its own Fields", e.Name)
		}
	}
	for _, filt := range e.Filters {
		if !fieldBelongsTo(filt.Field, e.Fields) {
			return fmt.Errorf("ir: entity %q has a filter field that is not an element of its own Fields", e.Name)
		}
	}
	for _, idx := range e.Indexes {
		for _, col := range idx.Columns {
			if !fieldBelongsTo(col.Field, e.Fields) {
				return fmt.Errorf("ir: entity %q has a declared index column that is not an element of its own Fields", e.Name)
			}
		}
	}
	for _, rel := range e.Relations {
		if !entityBelongsTo(rel.Target, s.Entities) {
			return fmt.Errorf("ir: entity %q relation %q target is not an element of Schema.Entities", e.Name, rel.Name)
		}
		// spec §2.2: "every Relation that exists carries a valid
		// TargetSpan." A Relation is only ever meant to be appended once
		// its `target:` has been read *and* resolved to a real entity
		// (internal/parse/relation.go's resolvePendingRelation returns
		// before appending one otherwise) -- so a zero TargetSpan can only
		// mean a Relation was built some other way, bypassing that rule.
		if !rel.TargetSpan.IsValid() {
			return fmt.Errorf("ir: entity %q relation %q has a zero TargetSpan (spec §2.2)", e.Name, rel.Name)
		}
	}
	return nil
}

// fieldBelongsTo reports whether target is the exact *Field held at some
// element of fields, by pointer identity.
func fieldBelongsTo(target *Field, fields []*Field) bool {
	for _, f := range fields {
		if f == target {
			return true
		}
	}
	return false
}

// entityBelongsTo reports whether target is the exact *Entity held at some
// element of entities, by pointer identity.
func entityBelongsTo(target *Entity, entities []*Entity) bool {
	for _, e := range entities {
		if e == target {
			return true
		}
	}
	return false
}
