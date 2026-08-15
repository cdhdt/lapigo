package parse

import (
	"testing"

	"github.com/goccy/go-yaml/parser"

	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestFreezeOrReportBug_CatchesAResolverBugParseDiagnosticsCannotSee is the
// regression test for the Tier 6 mutation-testing gap: "the Freeze
// self-check has zero coverage, yet defect 11 proved it is user-reachable;
// deleting the call survives." Fixing defect 11 (resolveVersion) closed the
// last invariant a user's schema could actually trigger, which makes
// Freeze -- by design -- unreachable through any schema this package's own
// resolver produces from valid input once resolveSchema itself reports zero
// diagnostics. The only way left to prove freezeOrReportBug's call to
// Freeze is load-bearing is to hand it a schema that is real (built by
// resolveSchema, the same function Parse uses) but then deliberately
// broken, simulating a bug in this package that resolveSchema's own
// diagnostics did not catch -- exactly the scenario the "internal: lapigo
// produced an invalid schema" message exists for.
func TestFreezeOrReportBug_CatchesAResolverBugParseDiagnosticsCannotSee(t *testing.T) {
	astFile, err := parser.ParseBytes([]byte("entities:\n  article:\n    fields:\n      id: { type: uuid, pk: true }\n"), parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	r := &resolver{file: "lapigo.yaml"}
	schema := r.resolveSchema(astFile.Docs[0].Body)
	if r.diags.HasErrors() {
		t.Fatalf("resolveSchema diags = %+v, want none", r.diags)
	}
	if err := schema.Freeze(); err != nil {
		t.Fatalf("Freeze() on the unmodified schema = %v, want nil", err)
	}

	// Break the identity invariant Freeze exists to catch: point Entity.PK
	// at a field that is not the one marked PK -- exactly the class of bug
	// defect 1 in the original review was about, reproduced here directly
	// rather than through any input a schema author could write.
	decoy := &ir.Field{Name: source.Bare("decoy"), PK: false}
	schema.Entities[0].PK = decoy

	got, diags := freezeOrReportBug(r, schema)

	if got != nil {
		t.Fatalf("schema = %+v, want nil", got)
	}
	if len(diags) != 1 {
		t.Fatalf("diags = %+v, want exactly 1", diags)
	}
	want := "internal: lapigo produced an invalid schema; this is a bug in lapigo, not your schema file: " +
		`ir: entity "article" PK does not point at the field marked PK`
	if diags[0].Message != want {
		t.Fatalf("Message = %q, want %q", diags[0].Message, want)
	}
}
