package ir

import "testing"

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

// TestSortKey_HoldsResolvedField (a Go-language-guarantee tautology: build
// f, assign it to SortKey.Field, assert they are equal) has been removed.
// It would pass unchanged the moment defect 1 corrupted every relation by
// retargeting after a sort — see TestSchema_Freeze_SurvivesAppendAndSort in
// freeze_test.go, which actually exercises the append-then-sort sequence
// that breaks identity under a value slice.
