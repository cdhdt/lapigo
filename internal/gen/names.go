package gen

import (
	"sort"

	"github.com/cdhdt/lapigo/internal/ir"
)

// This file is spec §5.6's declaration table, written as code.
//
// It exists to close the debt spec §10 records against step 3.
// internal/validate's reservedMethodNames rejects a schema field whose Go
// name would collide with a method the templates generate, and that list was
// asserted from the specification text because the templates it names did not
// exist. Nothing linked the two: a method added to a template without a
// matching entry there produces a schema that validates clean and fails to
// compile only once regenerated.
//
// §10's original mechanism -- "derive the reserved set from the templates" --
// is not achievable. Template files are text/template text, not parseable Go,
// and the identifier a template emits is a function of runtime data
// (`type {{.GoName}}Store struct`), so no static read of the template text
// yields "ArticleStore". The derivation runs the other way: render the
// fixture matrix, parse the OUTPUT with go/ast, and assert set equality per
// package in both directions (names_test.go).
//
// Two properties of the set are load-bearing (spec §5.6):
//
//   - Collisions are compared WITHIN a package, never across. Article in
//     model and ArticleStore in store are different packages and do not
//     collide. "Reject, never mangle" licenses rejecting an ambiguous schema;
//     it does not license rejecting a legal one, and a cross-package
//     comparison would.
//   - NoopEHooks is a PREFIX form. An implementation that asks "does this
//     name end in one of the known suffixes" is wrong, silently, for exactly
//     one entry in the table -- the one whose entity name lands at the end.
//     Nothing here matches on suffixes; every name is built from its parts.
//
// What this does NOT yet check, and when it will: internal/validate's
// reservedMethodNames is deliberately a superset of what is emitted (spec
// §5.6), so the remaining half of the link is the containment
// "every method name emitted here is reserved there". Today it is still
// vacuously true, and step 6 (hooks) does not change that: reservedMethodNames
// is scoped to methods on the model, CreateInput and UpdateInput types --
// the structs that carry an entity's fields as Go struct fields, per its own
// doc comment -- and a hook lives on <Entity>Hooks and Noop<Entity>Hooks,
// neither of which carries a field. §6.3's BeforeCreate has no collision
// surface with a schema field named "before_create": they are methods on
// different types in a different package. Step 7's Validate is the first
// method this file will predict on a covered receiver (CreateInput,
// UpdateInput), and is therefore the step that must add the containment
// check and the first one at which it could fail; nothing here can assert it
// earlier without recreating, in this package, the very list it would be
// checking.

// declarationSet returns, per generated package, every top-level declaration
// the templates emit for s -- types, functions, variables, constants and
// methods, exported or not (spec §5.6's definition of "declaration").
//
// A method is keyed "Receiver.Method". Bare method names would make every
// entity's Validate the same entry, which is exactly the resolution the
// equality test needs not to lose.
//
// Only packages whose templates exist are here. A package that does not yet
// render contributes no rows and contributes no fixture (spec §5.6): its
// share of the table lives in pendingDeclarations until the commit that adds
// its templates moves it, and the test that a pending name is absent from
// the output is what forces that move to happen in the same change.
func declarationSet(s *ir.Schema) map[string][]string {
	byPackage := map[string][]string{}
	if s == nil {
		return byPackage
	}

	// model, as rendered by templates/model_entity.tmpl and
	// templates/model_optional.tmpl: the fixed Optional[T] and its six
	// methods (spec §6.5, §5.6's model fixed row), always present; the
	// entity struct and, per enum field, a generated type plus one constant
	// per declared member; and, gated on HasCreate()/HasUpdate(), the
	// ECreateInput/EUpdateInput struct TYPES that spec §6.3's hook
	// signatures require to exist (spec §10's amended step 6 row -- see
	// input.go's own doc comment). Their Validate methods are not here:
	// Validate is step 7's (issue #28) and stays in pendingDeclarations,
	// below, until that commit.
	model := []string{
		"Optional",
		"Optional.Present",
		"Optional.IsNull",
		"Optional.Get",
		"Optional.Set",
		"Optional.SetNull",
		"Optional.UnmarshalJSON",
	}
	for _, e := range s.Entities {
		g := e.GoName
		model = append(model, g)
		for _, f := range enumFields(e) {
			model = append(model, f.EnumGoType)
			for _, v := range f.EnumValues {
				model = append(model, f.EnumGoType+v.GoName)
			}
		}
		if e.HasCreate() {
			model = append(model, g+"CreateInput")
		}
		if e.HasUpdate() {
			model = append(model, g+"UpdateInput")
		}
	}
	byPackage[packageModel] = sorted(model)

	// hooks, as rendered by templates/hooks_error.tmpl and
	// templates/hooks_entity.tmpl (step 6): the fixed Error type and
	// NewValidationError (spec §6.4, §5.6's hooks fixed row), always
	// present, plus per entity with at least one write operation the
	// <Entity>Hooks interface, its embeddable Noop<Entity>Hooks, and one
	// Before/After/AfterCommitted method trio per generated write operation
	// (spec §6.3, §5.6's hooks conditional row).
	hooks := []string{"Error", "Error.Error", "NewValidationError"}
	for _, e := range s.Entities {
		g := e.GoName
		if !e.HasCreate() && !e.HasUpdate() && !e.HasDelete() {
			continue
		}
		noop := "Noop" + g + "Hooks"
		hooks = append(hooks, g+"Hooks", noop)
		for _, op := range writeOperationsOf(e) {
			hooks = append(hooks,
				noop+".Before"+op,
				noop+".After"+op,
				noop+".After"+op+"Committed",
			)
		}
	}
	byPackage[packageHooks] = sorted(hooks)

	// store, as rendered by templates/store_cursor.tmpl (step 7's cursor
	// codec, spec §7.4): per entity that declares `list`, its fingerprint
	// constant, its wire and decoded cursor types, and its encoder and
	// decoder; plus the fixed envelope, encoder and decoder they are built
	// on.
	//
	// The fixed names are appended only when at least one entity lists,
	// because the file itself is emitted only then (planStoreCursorFiles's
	// doc comment says why it is not emitted unconditionally the way
	// model/optional.go is). Predicting them unconditionally would fail
	// direction 2 of the equality test on a schema that never paginates --
	// no_list is exactly that fixture.
	//
	// The rest of the package -- <Entity>Store and its methods,
	// <Entity>ListQuery, scan<Entity> -- is still pendingDeclarations', and
	// so is httpapi in full.
	var store []string
	for _, e := range s.Entities {
		if !e.HasList() {
			continue
		}
		g := e.GoName
		p := lowerFirst(g)
		store = append(store,
			p+"CursorFingerprint",
			p+"CursorKeys",
			p+"Cursor",
			"encode"+g+"Cursor",
			"decode"+g+"Cursor",
		)
	}
	if len(store) != 0 {
		store = append(store,
			"cursorFormatVersion",
			"ErrInvalidCursor",
			"cursorEnvelope",
			"encodeCursor",
			"decodeCursorKeys",
		)
		byPackage[packageStore] = sorted(store)
	}

	return byPackage
}

// pendingDeclarations returns, per generated package, the declarations spec
// §5.6 predicts whose templates do not exist yet.
//
// It is not decoration. names_test.go asserts that none of these names
// appears in the rendered output, so the commit that adds a template for one
// of them fails until it moves the row into declarationSet -- which is the
// only moment at which the table and the templates can be written together
// and known to agree.
//
// The conditions are spec §5.6's own and are not decoration either. The
// fixture matrix requires a fixture with each `endpoints:` member absent, so
// an unconditional row would predict, say, ArticleCreateInput for an entity
// that declares no create.
//
// What this list deliberately does NOT enumerate: the method sets of the
// fixed per-package types the spec names without naming their methods --
// httpapi's router constructor and request-id middleware (spec §6.2,
// §6.9.6). Their identifiers are not stated anywhere yet, so predicting them
// here would be inventing a contract rather than recording one; they enter
// the table with the commit that names them. Store's cursor codec was the
// other entry of that list until issue #28 named its identifiers, which is
// the move this mechanism exists to force: they are in declarationSet now,
// gated on `list` exactly as the template gates them.
func pendingDeclarations(s *ir.Schema) map[string][]string {
	byPackage := map[string][]string{}
	if s == nil {
		return byPackage
	}

	// model's remaining rows: the two input types' Validate methods (spec
	// §5.6, §6.5). The types themselves, and the fixed Optional[T], are in
	// declarationSet now -- step 6 needed them to exist for hooks to
	// type-check (spec §10's amended step 6 row) -- but Validate is step
	// 7's (issue #28), the method that actually enforces §6.5's "Question
	// 2", and nothing about hooks needs it.
	var model []string
	// store's remaining rows are the store type itself and its methods.
	// Its fixed row -- the cursor codec of spec §7.4 -- moved into
	// declarationSet with issue #28, which is why nothing fixed is left
	// here.
	var store []string
	// httpapi's fixed row: respondError is the one identifier spec §6.7
	// names outright.
	httpapi := []string{"respondError"}

	for _, e := range s.Entities {
		g := e.GoName

		if e.HasCreate() {
			model = append(model, g+"CreateInput.Validate")
		}
		if e.HasUpdate() {
			model = append(model, g+"UpdateInput.Validate")
		}

		store = append(store, g+"Store", "scan"+g)
		if e.HasList() {
			store = append(store, g+"ListQuery", g+"Store.List")
		}
		if e.HasGet() {
			store = append(store, g+"Store.Get")
		}
		if e.HasCreate() {
			store = append(store, g+"Store.Create")
		}
		if e.HasUpdate() {
			store = append(store, g+"Store.Update")
		}
		if e.HasDelete() {
			store = append(store, g+"Store.Delete")
		}
	}

	byPackage[packageModel] = sorted(model)
	byPackage[packageStore] = sorted(store)
	byPackage[packageHTTPAPI] = sorted(httpapi)
	return byPackage
}

// writeOperationsOf returns the write operations e generates hooks for, in a
// fixed order (spec §6.3: one method trio per generated write operation, and
// no List or Get hooks in phase 1).
func writeOperationsOf(e *ir.Entity) []string {
	var out []string
	if e.HasCreate() {
		out = append(out, "Create")
	}
	if e.HasUpdate() {
		out = append(out, "Update")
	}
	if e.HasDelete() {
		out = append(out, "Delete")
	}
	return out
}

// sorted returns names sorted and deduplicated. Deduplicated because two
// entities of one schema can legitimately contribute the same fixed name,
// and a set is what both directions of the equality test compare.
func sorted(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := make([]string, 0, len(names))
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
