package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

func TestSortSpec_Direction(t *testing.T) {
	tests := []struct {
		name string
		desc bool
		want string
	}{
		{"ascending", false, "ASC"},
		{"descending", true, "DESC"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := SortSpec{Desc: tt.desc}
			if got := s.Direction(); got != tt.want {
				t.Errorf("Direction() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestSortKey_HoldsResolvedField documents that SortKey.Field is a resolved
// *Field pointer, not a name — the single most important IR failure mode per
// spec §2.2.
func TestSortKey_HoldsResolvedField(t *testing.T) {
	f := &Field{Name: source.Bare("id"), Column: "id", Type: FieldTypeUUID, PK: true}
	key := SortKey{Field: f}

	if key.Field != f {
		t.Errorf("SortKey.Field = %p, want %p (same pointer as the resolved field)", key.Field, f)
	}
	if key.Field.Column != "id" {
		t.Errorf("SortKey.Field.Column = %q, want %q", key.Field.Column, "id")
	}
}
