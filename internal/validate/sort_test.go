package validate

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/diag"
	"github.com/cdhdt/lapigo/internal/ir"
	"github.com/cdhdt/lapigo/internal/source"
)

// TestValidateSortKeyEligibility_UnknownFieldType is the regression test for
// the defensive default branch in validateSortKeyEligibility: a FieldType
// outside the closed enumeration internal/ir defines. internal/parse never
// produces one from real input (every `type:` keyword it accepts maps to a
// known ir.FieldType, see parse/field.go's fieldTypeKeywords), so this case
// is unreachable through the parse.Parse -> Validate path every golden
// fixture in this package uses -- exercised here directly, the same way
// internal/ir's own
// TestField_IsSortEligible_UnknownTypeHasNoDefaultBranch exercises
// IsSortEligible's matching default case, so a future FieldType added to
// the enum without a branch here cannot silently fall through to "eligible".
func TestValidateSortKeyEligibility_UnknownFieldType(t *testing.T) {
	e := &ir.Entity{Name: "widget"}
	f := &ir.Field{
		Name: source.NewAt("mystery", source.Pos{Line: 3, Column: 5}, source.Pos{Line: 3, Column: 12}),
		Type: ir.FieldType(99),
	}
	k := ir.SortKey{Field: f, Span: source.NewSpan(source.Pos{Line: 3, Column: 5}, source.Pos{Line: 3, Column: 12})}

	var diags diag.Diagnostics
	validateSortKeyEligibility(e, k, "lapigo.yaml", &diags)

	if len(diags) != 1 {
		t.Fatalf("len(diags) = %d, want 1: %+v", len(diags), diags)
	}
	got := diags[0]
	want := diag.Diagnostic{
		Severity:  diag.Error,
		File:      "lapigo.yaml",
		Pos:       source.Pos{Line: 3, Column: 5},
		EndColumn: 12,
		Message:   `sort key "mystery" may not be used: field type FieldType(99) is not a known FieldType, so its sort eligibility cannot be determined`,
		Hint:      "remove `mystery` from `sort`",
	}
	if got != want {
		t.Errorf("validateSortKeyEligibility diagnostic =\n%+v\nwant:\n%+v", got, want)
	}
}
