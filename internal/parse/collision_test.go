package parse

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestBuildEntity_BelongsToColumnCollisionWithScalarField is the regression
// test for defect 5: `author: { type: belongsTo, target: article }`
// produces an FK field with Column "author_id" and GoName "AuthorID"; a
// sibling scalar field explicitly named `author_id` produces the identical
// Column and GoName, and nothing compared them because Freeze only compares
// Name.Value ("author" != "author_id"). The DDL emitter would emit the
// column twice and the generated model struct would declare the same Go
// field twice -- neither compiles.
func TestBuildEntity_BelongsToColumnCollisionWithScalarField(t *testing.T) {
	src := "entities:\n" +
		"  article:\n" +
		"    fields:\n" +
		"      id: { type: uuid, pk: true }\n" +
		"      author: { type: belongsTo, target: article }\n" +
		"      author_id: { type: uuid }\n"
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})

	wantColumn := `field "author_id" and field "author" both use column "author_id"`
	wantGoName := `field "author_id" and field "author" both produce Go field name "AuthorID"`
	var haveColumn, haveGoName bool
	for _, d := range diags {
		if d.Message == wantColumn {
			haveColumn = true
		}
		if d.Message == wantGoName {
			haveGoName = true
		}
	}
	if !haveColumn {
		t.Errorf("no column-collision diagnostic among: %+v", diags)
	}
	if !haveGoName {
		t.Errorf("no GoName-collision diagnostic among: %+v", diags)
	}
}

// TestBuildEntity_InvalidFieldNameDoesNotTriggerSpuriousGoNameCollision is
// review finding F2 on PR #39: goName's separator rule was generalised from
// "_" alone to any run of non-alphanumeric characters (issue #24, so that
// two enum values like "in-progress" and "in_progress" collide the way spec
// §5.6 requires), but requireIdentifier's rejection of an invalid field name
// does not stop buildField from still calling goName on it -- every call
// site (buildEntity, buildField, buildRelationField) discards
// requireExportableName's bool and keeps building. Before this fix,
// "a-b" (already rejected as "not a valid identifier") and "a_b" (a
// perfectly legal field) both produced GoName "AB" under the wider
// separator rule, so checkFieldCollisions blamed the *valid* field "a_b"
// for a collision that exists only because "a-b" was mangled past a check
// it had already failed -- exactly the kind of confusing, misleading
// diagnostic CLAUDE.md's "compiler-grade error messages" rule exists to
// prevent.
func TestBuildEntity_InvalidFieldNameDoesNotTriggerSpuriousGoNameCollision(t *testing.T) {
	src := "entities:\n" +
		"  article:\n" +
		"    fields:\n" +
		"      id:    { type: uuid, pk: true }\n" +
		"      \"a-b\": { type: string }\n" +
		"      a_b:   { type: string }\n"
	_, diags := Parse(source.File{Name: "lapigo.yaml", Src: []byte(src)})

	wantInvalid := `field name "a-b" is not a valid identifier`
	spurious := `field "a_b" and field "a-b" both produce Go field name "AB"`
	var haveInvalid, haveSpurious bool
	for _, d := range diags {
		if d.Message == wantInvalid {
			haveInvalid = true
		}
		if d.Message == spurious {
			haveSpurious = true
		}
	}
	if !haveInvalid {
		t.Errorf("no invalid-identifier diagnostic among: %+v", diags)
	}
	if haveSpurious {
		t.Errorf("spurious GoName-collision diagnostic present, blaming the validly-named field \"a_b\" for \"a-b\"'s own already-reported defect: %+v", diags)
	}
	if len(diags) != 1 {
		t.Errorf("diags = %+v, want exactly 1 (the invalid-identifier diagnostic alone)", diags)
	}
}
