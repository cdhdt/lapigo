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
