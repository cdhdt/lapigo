package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

func TestField_IsSortEligible_Nullable(t *testing.T) {
	f := &Field{Name: source.Bare("created_at"), Type: FieldTypeTimestamp, Nullable: true}

	ok, reason := f.IsSortEligible()

	if ok {
		t.Fatal("IsSortEligible() = true, want false for a nullable field")
	}
	if reason == "" {
		t.Error("IsSortEligible() returned no reason for an ineligible field")
	}
}

func TestField_IsSortEligible_Decimal(t *testing.T) {
	f := &Field{Name: source.Bare("price"), Type: FieldTypeDecimal}

	ok, reason := f.IsSortEligible()

	if ok {
		t.Fatal("IsSortEligible() = true, want false for a decimal field")
	}
	if reason == "" {
		t.Error("IsSortEligible() returned no reason for an ineligible field")
	}
}

func TestField_IsSortEligible_JSON(t *testing.T) {
	f := &Field{Name: source.Bare("meta"), Type: FieldTypeJSON}

	ok, reason := f.IsSortEligible()

	if ok {
		t.Fatal("IsSortEligible() = true, want false for a json field")
	}
	if reason == "" {
		t.Error("IsSortEligible() returned no reason for an ineligible field")
	}
}

func TestField_IsSortEligible_Eligible(t *testing.T) {
	tests := []struct {
		name string
		f    *Field
	}{
		{"non-nullable timestamp", &Field{Name: source.Bare("created_at"), Type: FieldTypeTimestamp}},
		{"non-nullable uuid pk", &Field{Name: source.Bare("id"), Type: FieldTypeUUID, PK: true}},
		{"non-nullable string", &Field{Name: source.Bare("slug"), Type: FieldTypeString}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ok, reason := tt.f.IsSortEligible()
			if !ok {
				t.Errorf("IsSortEligible() = false (reason %q), want true", reason)
			}
			if reason != "" {
				t.Errorf("IsSortEligible() reason = %q, want empty when eligible", reason)
			}
		})
	}
}

func TestDefaultValue_Now(t *testing.T) {
	d := DefaultValue{Kind: DefaultNow}
	if d.Kind != DefaultNow {
		t.Errorf("Kind = %v, want DefaultNow", d.Kind)
	}
}

func TestDefaultValue_Literal(t *testing.T) {
	d := DefaultValue{Kind: DefaultLiteral, Literal: "draft"}
	if d.Literal != "draft" {
		t.Errorf("Literal = %q, want %q", d.Literal, "draft")
	}
}
