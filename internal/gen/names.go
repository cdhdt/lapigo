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
// "every method name emitted here is reserved there". Today it is vacuously
// true, because no template emits a method -- every method in spec §5.6's
// table is in pendingDeclarations below, and the test that a pending name is
// absent from the output is what keeps that true. The step that first emits
// one (step 6's hooks, step 7's Validate) is the step that must add the
// containment check, and it is also the first step at which the check could
// fail; nothing here can assert it earlier without recreating, in this
// package, the very list it would be checking.

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

	// model, as rendered by templates/model_entity.tmpl today: the entity
	// struct, and per enum field a generated type plus one constant per
	// declared member. Both conditional model rows (ECreateInput,
	// EUpdateInput and their Validate methods) and model's fixed row
	// (Optional[T]) are still pending, below.
	var model []string
	for _, e := range s.Entities {
		model = append(model, e.GoName)
		for _, f := range enumFields(e) {
			model = append(model, f.EnumGoType)
			for _, v := range f.EnumValues {
				model = append(model, f.EnumGoType+v.GoName)
			}
		}
	}
	if len(model) != 0 {
		byPackage[packageModel] = sorted(model)
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
// store's cursor encoder, decoder and decode-error type (spec §7.4), and
// httpapi's router constructor and request-id middleware (spec §6.2, §6.9.6).
// Their identifiers are not stated anywhere yet, so predicting them here
// would be inventing a contract rather than recording one; they enter the
// table with the commit that names them.
func pendingDeclarations(s *ir.Schema) map[string][]string {
	byPackage := map[string][]string{}
	if s == nil {
		return byPackage
	}

	// model's remaining rows: the two input types and their Validate
	// methods (spec §5.6, §6.5), plus the fixed Optional[T] every input
	// member is built from and the six methods spec §6.5 states for it.
	model := []string{
		"Optional",
		"Optional.Present",
		"Optional.IsNull",
		"Optional.Get",
		"Optional.Set",
		"Optional.SetNull",
		"Optional.UnmarshalJSON",
	}
	// store's fixed row is the cursor codec, whose identifiers spec §7.4
	// does not name; see the doc comment.
	var store []string
	// hooks' fixed row (spec §6.4): the typed error a hook returns to carry
	// its own status into respondError.
	hooks := []string{"Error", "Error.Error", "NewValidationError"}
	// httpapi's fixed row: respondError is the one identifier spec §6.7
	// names outright.
	httpapi := []string{"respondError"}

	for _, e := range s.Entities {
		g := e.GoName

		if e.HasCreate() {
			model = append(model, g+"CreateInput", g+"CreateInput.Validate")
		}
		if e.HasUpdate() {
			model = append(model, g+"UpdateInput", g+"UpdateInput.Validate")
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

		// hooks exists for an entity only once it has a write operation to
		// hook: spec §5.6's hooks row has nothing in its "always" column,
		// and there are no List or Get hooks in phase 1 (spec §6.3).
		if e.HasCreate() || e.HasUpdate() || e.HasDelete() {
			// NoopEHooks is a prefix form, built here from its parts.
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
	}

	byPackage[packageModel] = sorted(model)
	byPackage[packageStore] = sorted(store)
	byPackage[packageHooks] = sorted(hooks)
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
