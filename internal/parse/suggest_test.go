package parse

import "testing"

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"type", "type", 0},
		{"type", "typ", 1},
		{"required", "requird", 1},
		{"target", "targt", 1},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSuggest_ReturnsClosestWithinThreshold(t *testing.T) {
	known := []string{"type", "pk", "required", "unique", "max", "values", "target", "on_delete", "default", "readonly", "immutable", "version"}

	if got := suggest("requird", known); got != "required" {
		t.Errorf("suggest(%q) = %q, want %q", "requird", got, "required")
	}
	if got := suggest("typ", known); got != "type" {
		t.Errorf("suggest(%q) = %q, want %q", "typ", got, "type")
	}
}

func TestSuggest_ReturnsEmptyWhenNothingIsClose(t *testing.T) {
	known := []string{"type", "pk", "required"}

	if got := suggest("completely_unrelated_word", known); got != "" {
		t.Errorf("suggest(completely unrelated) = %q, want empty", got)
	}
}
