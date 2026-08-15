package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

func TestFilterOp_String(t *testing.T) {
	if got, want := FilterOpEq.String(), "eq"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestFilterOp_String_Unknown(t *testing.T) {
	var op FilterOp = 999
	if got := op.String(); got == "" {
		t.Error("String() on an unknown FilterOp returned empty string, want a diagnostic placeholder")
	}
}

// TestFilter_HoldsResolvedField documents that Filter.Field is a *Field
// pointer, not a name: a template rendering a filter comparison needs the
// field's Go type, column and nullability without a lookup.
func TestFilter_HoldsResolvedField(t *testing.T) {
	f := &Field{Name: source.Bare("status"), Column: "status", Type: FieldTypeEnum}
	filter := Filter{Field: f, Op: FilterOpEq}

	if filter.Field != f {
		t.Errorf("Filter.Field = %p, want %p (same pointer as the resolved field)", filter.Field, f)
	}
	if filter.Field.Column != "status" {
		t.Errorf("Filter.Field.Column = %q, want %q", filter.Field.Column, "status")
	}
}
