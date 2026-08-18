package ir

import (
	"sort"
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// buildResolvedSchema returns two entities wired together the way a resolver
// leaves them: component's PK is the target of widget's relation, and
// widget's own PK, sort key and filter are resolved pointers into its own
// Fields. Entities is deliberately built out of alphabetical order ("widget"
// sorts after "component") so the tests below can both check that Freeze
// rejects an unsorted Schema and prove that sorting it correctly does not
// retarget anything.
func buildResolvedSchema() (schema *Schema, widget, component *Entity) {
	componentID := &Field{Name: source.Bare("id"), Column: "id", Type: FieldTypeUUID, PK: true}
	component = &Entity{
		Name:   "component",
		GoName: "Component",
		Table:  "components",
		Fields: []*Field{componentID},
		PK:     componentID,
	}

	widgetID := &Field{Name: source.Bare("id"), Column: "id", Type: FieldTypeUUID, PK: true}
	createdAt := &Field{Name: source.Bare("created_at"), Column: "created_at", Type: FieldTypeTimestamp}
	status := &Field{Name: source.Bare("status"), Column: "status", Type: FieldTypeEnum, EnumGoType: "WidgetStatus"}

	widget = &Entity{
		Name:   "widget",
		GoName: "Widget",
		Table:  "widgets",
		Fields: []*Field{widgetID, createdAt, status},
		PK:     widgetID,
		Sort: SortSpec{
			Desc: true,
			Keys: []SortKey{{Field: createdAt}, {Field: widgetID}},
		},
		Filters: []Filter{{Field: status, Op: FilterOpEq}},
		Relations: []Relation{{
			Name: "component", GoName: "Component", Target: component, Column: "component_id",
			TargetSpan: source.Span{Start: source.Pos{Line: 5, Column: 20}, End: source.Pos{Line: 5, Column: 29}},
		}},
	}

	schema = &Schema{Entities: []*Entity{widget, component}} // deliberately unsorted
	return schema, widget, component
}

func TestSchema_Freeze_RejectsUnsortedEntities(t *testing.T) {
	schema, _, _ := buildResolvedSchema()

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: Entities is not sorted by name")
	}
}

// TestSchema_Freeze_SurvivesAppendAndSort is the test defect 1 exists for.
// Schema.Entities and Entity.Fields switched from value slices to pointer
// slices specifically because an append can reallocate a value slice's
// backing array, and sorting a value slice silently retargets any pointer
// taken into it — a resolved Relation.Target would end up naming a
// different entity with no crash and no nil. This test appends a field and
// sorts the entities, the two operations that broke value slices, and then
// asserts every previously-resolved pointer still names the right value.
func TestSchema_Freeze_SurvivesAppendAndSort(t *testing.T) {
	schema, widget, component := buildResolvedSchema()

	sort.Slice(schema.Entities, func(i, j int) bool {
		return schema.Entities[i].Name < schema.Entities[j].Name
	})
	if schema.Entities[0] != component || schema.Entities[1] != widget {
		t.Fatalf("sort did not reorder as expected: got [%s, %s]", schema.Entities[0].Name, schema.Entities[1].Name)
	}

	extra := &Field{Name: source.Bare("label"), Column: "label", Type: FieldTypeString}
	widget.Fields = append(widget.Fields, extra)

	if err := schema.Freeze(); err != nil {
		t.Fatalf("Freeze() after append and sort = %v, want nil", err)
	}

	// The pointers resolved before the append and sort still name the same
	// values by identity, not merely by matching Name — a value-slice
	// implementation would fail one or more of these.
	if widget.PK != widget.Fields[0] {
		t.Error("widget.PK no longer points at widget.Fields[0] after append and sort")
	}
	if widget.Sort.Keys[1].Field != widget.PK {
		t.Error("widget.Sort's last key no longer points at widget.PK after append and sort")
	}
	if widget.Filters[0].Field != widget.Fields[2] {
		t.Error("widget.Filters[0].Field was retargeted by the append and sort")
	}
	if widget.Relations[0].Target != component {
		t.Error("widget.Relations[0].Target was retargeted by the sort: no longer the same *Entity as component")
	}
	if widget.Fields[len(widget.Fields)-1] != extra {
		t.Error("the appended field is not the same pointer that was appended")
	}
}

func TestSchema_Freeze_Success(t *testing.T) {
	schema, _, _ := buildResolvedSchema()
	sort.Slice(schema.Entities, func(i, j int) bool {
		return schema.Entities[i].Name < schema.Entities[j].Name
	})

	if err := schema.Freeze(); err != nil {
		t.Fatalf("Freeze() = %v, want nil for a well-formed schema", err)
	}
}

// TestSchema_Freeze_NilReceiver pins that Freeze on a nil *Schema returns an
// error rather than panicking (CLAUDE.md forbids panics in library code).
func TestSchema_Freeze_NilReceiver(t *testing.T) {
	var schema *Schema

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() on a nil Schema = nil, want an error, not a panic")
	}
}

func TestSchema_Freeze_DuplicatePK(t *testing.T) {
	a := &Field{Name: source.Bare("id"), PK: true}
	b := &Field{Name: source.Bare("uuid"), PK: true}
	e := &Entity{Name: "widget", Fields: []*Field{a, b}, PK: a}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: two fields marked PK, spec §3.1 allows exactly one")
	}
}

func TestSchema_Freeze_NoPK(t *testing.T) {
	a := &Field{Name: source.Bare("id")}
	e := &Entity{Name: "widget", Fields: []*Field{a}}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: no field marked PK")
	}
}

func TestSchema_Freeze_TooManyVersionFields(t *testing.T) {
	id := &Field{Name: source.Bare("id"), PK: true}
	v1 := &Field{Name: source.Bare("v1"), Version: true}
	v2 := &Field{Name: source.Bare("v2"), Version: true}
	e := &Entity{Name: "widget", Fields: []*Field{id, v1, v2}, PK: id}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: two fields marked Version, spec §3.1 allows at most one")
	}
}

func TestSchema_Freeze_DuplicateFieldName(t *testing.T) {
	id := &Field{Name: source.Bare("id"), PK: true}
	dup1 := &Field{Name: source.Bare("name")}
	dup2 := &Field{Name: source.Bare("name")}
	e := &Entity{Name: "widget", Fields: []*Field{id, dup1, dup2}, PK: id}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal(`Freeze() = nil, want an error: duplicate field name "name"`)
	}
}

// TestSchema_Freeze_PKIdentityNotName proves the PK check is by pointer
// identity, not by name: decoy has the same Name and PK: true as the real
// field but is not an element of e.Fields, so Freeze must still reject it.
func TestSchema_Freeze_PKIdentityNotName(t *testing.T) {
	real := &Field{Name: source.Bare("id"), PK: true}
	decoy := &Field{Name: source.Bare("id"), PK: true}
	e := &Entity{Name: "widget", Fields: []*Field{real}, PK: decoy}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: PK points at a field with the same name but different identity")
	}
}

// TestSchema_Freeze_SortKeyNotOwnField proves the sort-key check is by
// identity, not by name.
//
// The decoy deliberately shares the real field's name. An earlier version of
// this test used a decoy named "created_at" against a field named "id", which
// proved only that a foreign field is rejected — rewriting fieldBelongsTo to
// compare names instead of pointers left it green. A same-name decoy is the
// only shape that discriminates.
func TestSchema_Freeze_SortKeyNotOwnField(t *testing.T) {
	id := &Field{Name: source.Bare("id"), PK: true}
	foreign := &Field{Name: source.Bare("id"), Type: FieldTypeUUID}
	e := &Entity{
		Name:   "widget",
		Fields: []*Field{id},
		PK:     id,
		Sort:   SortSpec{Keys: []SortKey{{Field: foreign}, {Field: id}}},
	}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: sort key field does not belong to the entity's own Fields")
	}
}

// TestSchema_Freeze_FilterFieldNotOwnField proves the filter check is by
// identity, not by name — hence the same-name decoy, for the reason spelled
// out on TestSchema_Freeze_SortKeyNotOwnField.
func TestSchema_Freeze_FilterFieldNotOwnField(t *testing.T) {
	id := &Field{Name: source.Bare("id"), PK: true}
	foreign := &Field{Name: source.Bare("id"), Type: FieldTypeUUID}
	e := &Entity{
		Name:    "widget",
		Fields:  []*Field{id},
		PK:      id,
		Filters: []Filter{{Field: foreign, Op: FilterOpEq}},
	}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: filter field does not belong to the entity's own Fields")
	}
}

// TestSchema_Freeze_RelationTargetNotInSchema proves the relation check is by
// identity, not by name. The decoy shares the schema entity's name, which is
// the case that actually matters: a Relation.Target retargeted by sorting
// points at a real entity whose name may well match what the resolver
// intended, and only pointer identity distinguishes it.
func TestSchema_Freeze_RelationTargetNotInSchema(t *testing.T) {
	id := &Field{Name: source.Bare("id"), PK: true}
	foreignEntity := &Entity{Name: "widget"} // same name as e, deliberately not in schema.Entities
	e := &Entity{
		Name:   "widget",
		Fields: []*Field{id},
		PK:     id,
		Relations: []Relation{{
			Name: "owner", Target: foreignEntity,
			// A valid TargetSpan, so this test fails only for the
			// membership defect it names -- not incidentally, for the
			// separate TargetSpan invariant TestSchema_Freeze_
			// RelationTargetSpanIsZero exists to pin.
			TargetSpan: source.Span{Start: source.Pos{Line: 1, Column: 1}, End: source.Pos{Line: 1, Column: 2}},
		}},
	}
	schema := &Schema{Entities: []*Entity{e}}

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: relation target is not an element of Schema.Entities")
	}
}

// TestSchema_Freeze_RelationTargetSpanIsZero is the regression test for spec
// §2.2's invariant: "every Relation that exists carries a valid TargetSpan."
// A Relation is only ever meant to be appended once its `target:` has been
// read *and* resolved -- see internal/parse/relation.go's
// resolvePendingRelation, which returns before appending a Relation whenever
// targetName is empty, and buildRelationField's own "target" case, which
// only ever sets targetName from a real, non-empty string token. A Relation
// with a zero TargetSpan is therefore not a value any correct resolver can
// produce; Freeze is the last gate that can catch one anyway (a hand-built
// *ir.Schema, or a resolver with a bug reintroduced later), matching the
// same "invariants live in code, not in comments" standard already applied
// to PK, Sort, Filter and Relation-membership above.
func TestSchema_Freeze_RelationTargetSpanIsZero(t *testing.T) {
	id := &Field{Name: source.Bare("id"), PK: true}
	targetID := &Field{Name: source.Bare("id"), PK: true}
	target := &Entity{Name: "user", Fields: []*Field{targetID}, PK: targetID}
	e := &Entity{
		Name:   "widget",
		Fields: []*Field{id},
		PK:     id,
		Relations: []Relation{{
			Name:   "owner",
			Target: target,
			// TargetSpan deliberately left zero: every other invariant this
			// Relation could violate (membership, PK bookkeeping on either
			// entity) is satisfied, so a failure here can only be the
			// TargetSpan check.
		}},
	}
	schema := &Schema{Entities: []*Entity{e, target}}
	sort.Slice(schema.Entities, func(i, j int) bool {
		return schema.Entities[i].Name < schema.Entities[j].Name
	})

	if err := schema.Freeze(); err == nil {
		t.Fatal("Freeze() = nil, want an error: relation has a zero TargetSpan (spec §2.2)")
	}
}
