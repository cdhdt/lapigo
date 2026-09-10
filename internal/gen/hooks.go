package gen

import (
	"fmt"
	"sort"

	"github.com/cdhdt/lapigo/internal/ir"
)

// This file is step 6's share of the pipeline (spec §6.2 escape hatch 1,
// §6.3, §6.4): the hooks package's fixed file (hooks.Error and
// NewValidationError) and, per entity with at least one write operation,
// the <Entity>Hooks interface and its embeddable no-op.
//
// modelImportPath returns the import path a generated file uses to reach
// the model package: modulePath, the target project's own module path
// (plan's doc comment explains why plan needs one at all), plus the fixed
// internal/gen/model directory every generated project uses (spec §6.1).
func modelImportPath(modulePath string) string {
	return modulePath + "/" + genRoot + "/" + packageModel
}

// hookOperation is one write operation's method trio -- BeforeX, AfterX,
// AfterXCommitted (spec §6.3) -- with the two things that differ per
// operation already resolved to Go text, so the template only substitutes
// them (spec §5.1): which method-name suffix, and what BeforeX's operation
// -specific parameter looks like.
//
// AfterX and AfterXCommitted need no such parameter: both take the full row,
// *model.<Entity>, for every operation alike (spec §6.3) -- Delete included,
// since the store's `DELETE ... RETURNING` already has it loaded.
type hookOperation struct {
	// Name is the method-name suffix: "Create", "Update" or "Delete".
	Name string
	// BeforeParam is BeforeX's third parameter, name and type together, for
	// the interface and the user-facing declaration -- e.g.
	// "in *model.ArticleCreateInput" or "id pgtype.UUID" (spec §6.3:
	// BeforeDelete takes the id, never the row).
	BeforeParam string
	// BeforeArg is the same parameter's bare type, for the Noop
	// implementation's unnamed parameter list.
	BeforeArg string
}

// hookOperations returns e's hook operations, in the fixed order Create,
// Update, Delete, filtered to those e actually generates (spec §6.3, §5.6:
// the endpoint set gates which trio exists at all). It is a function in Go
// rather than three {{if}} blocks worth of parameter text in the template,
// because the shape of BeforeX's parameter is exactly the per-field
// decision spec §5.1 keeps out of template text.
func hookOperations(e *ir.Entity) []hookOperation {
	var out []hookOperation
	if e.HasCreate() {
		out = append(out, hookOperation{
			Name:        "Create",
			BeforeParam: "in *model." + e.GoName + "CreateInput",
			BeforeArg:   "*model." + e.GoName + "CreateInput",
		})
	}
	if e.HasUpdate() {
		out = append(out, hookOperation{
			Name:        "Update",
			BeforeParam: "in *model." + e.GoName + "UpdateInput",
			BeforeArg:   "*model." + e.GoName + "UpdateInput",
		})
	}
	if e.HasDelete() {
		out = append(out, hookOperation{
			Name:        "Delete",
			BeforeParam: "id " + e.PK.GoType(),
			BeforeArg:   e.PK.GoType(),
		})
	}
	return out
}

// planHooksFiles builds every hooks-package output file for s: the fixed
// error file, always present, and one per-entity file for every entity that
// generates at least one hook trio.
//
// The error file is planned first and the per-entity files follow in schema
// order (already sorted by name, spec §5.3) -- a fixed position for the
// package's one schema-independent file, ahead of the part that varies with
// the schema.
func planHooksFiles(s *ir.Schema, modulePath string) ([]OutputFile, error) {
	files := []OutputFile{planHooksErrorFile()}

	for _, e := range s.Entities {
		if !e.HasCreate() && !e.HasUpdate() && !e.HasDelete() {
			// Spec §5.6: hooks exists for an entity only once it has a
			// write operation to hook. An entity with none -- list_only's
			// note -- contributes no file at all.
			continue
		}
		f, err := planHooksEntityFile(e, modulePath)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

// planHooksErrorFile builds the OutputFile for hooks' one fixed
// declaration, hooks.Error and NewValidationError (spec §6.4, §5.6's hooks
// fixed row). It needs no Entity and no modulePath: nothing in it names a
// model type.
func planHooksErrorFile() OutputFile {
	imports := []string{"fmt", "net/http"}
	return OutputFile{
		Path:     genRoot + "/" + packageHooks + "/error.go",
		Package:  packageHooks,
		Imports:  imports,
		Template: "hooks/error.go",
		Data: fileData{
			Package: packageHooks,
			Imports: imports,
		},
	}
}

// planHooksEntityFile builds the OutputFile for e's hook interface and
// no-op implementation.
//
// One file per entity, named after the entity as written, mirroring
// planModelFile: Entity.Name has passed the parser's identifier grammar, so
// it is safe as a path element.
func planHooksEntityFile(e *ir.Entity, modulePath string) (OutputFile, error) {
	imports, err := hooksEntityImports(e, modulePath)
	if err != nil {
		return OutputFile{}, err
	}
	return OutputFile{
		Path:     genRoot + "/" + packageHooks + "/" + e.Name + ".go",
		Package:  packageHooks,
		Imports:  imports,
		Template: "hooks/entity.go",
		Data: fileData{
			Package: packageHooks,
			Imports: imports,
			Entity:  e,
		},
	}, nil
}

// hooksEntityImports returns the sorted, deduplicated import set of e's
// hooks file.
//
// context and pgx are needed unconditionally: planHooksEntityFile is only
// ever called for an entity with at least one hook trio, and every trio's
// BeforeX/AfterX pair takes a context.Context and a pgx.Tx (spec §6.3). The
// model import is needed for the same reason -- every trio's AfterX and
// AfterXCommitted take *model.<Entity>. A delete trio additionally needs
// whatever package the primary key's Go type lives in (pgtype, for a uuid
// key; nothing further for a plain string or numeric one) -- BeforeDelete
// is the one method that does not take the model row (spec §6.3).
func hooksEntityImports(e *ir.Entity, modulePath string) ([]string, error) {
	seen := map[string]bool{
		"context":                   true,
		pgxImport:                   true,
		modelImportPath(modulePath): true,
	}

	if e.HasDelete() {
		paths, err := importsForFieldType(e.PK.Type)
		if err != nil {
			return nil, fmt.Errorf("gen: entity %q hooks: primary key %q: %w", e.Name, e.PK.Name.Value, err)
		}
		for _, p := range paths {
			seen[p] = true
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	// Sorted before it leaves the function: derived from a map, whose
	// iteration order Go randomises per process (spec §5.3).
	sort.Strings(out)
	return out, nil
}
