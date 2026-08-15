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

// TestSuggest_BoundaryAtExactlyThreshold and
// TestSuggest_BoundaryOneBeyondThreshold pin the guard at
// bestDist > suggestThreshold down to the exact boundary, on either side.
// The old implementation capped its search radius at suggestThreshold
// itself (bestDist started at suggestThreshold+1 and only ever decreased),
// so the final "bestDist > suggestThreshold" check could only ever see a
// bestDist already <= suggestThreshold from an update, or the untouched
// initial value -- both cases already decided by best == "". Deleting the
// guard line changed nothing, which is what made
// TestSuggest_ReturnsEmptyWhenNothingIsClose pass for the wrong reason.
//
// These two tests use a known list whose single entry sits at exactly
// suggestThreshold, and one whose single entry sits at
// suggestThreshold+1 -- the true nearest match in both cases, found by
// scanning every candidate rather than only those already within radius
// suggestThreshold. A correct implementation must accept the first and
// reject the second; the old implementation, and any deletion of the final
// guard, cannot tell them apart with a single-candidate vocabulary because
// nothing else would have set bestDist yet.
func TestSuggest_BoundaryAtExactlyThreshold(t *testing.T) {
	// "xx" is distance 2 from "required" ("re" + 6 more edits)? No -- build
	// a deliberate distance-2 pair instead: "typex" vs "type" is distance 1;
	// use "tyxx" vs "type": t-y-x-x vs t-y-p-e is 2 substitutions.
	known := []string{"type"}
	if got := suggest("tyxx", known); got != "type" {
		t.Fatalf("suggest(%q, %q) = %q, want %q (distance == suggestThreshold must still suggest)", "tyxx", known, got, "type")
	}
}

func TestSuggest_BoundaryOneBeyondThreshold(t *testing.T) {
	// "txxx" vs "type": t-x-x-x vs t-y-p-e is 3 substitutions --
	// suggestThreshold+1 -- must not be suggested.
	known := []string{"type"}
	if got := suggest("txxx", known); got != "" {
		t.Fatalf("suggest(%q, %q) = %q, want empty (distance == suggestThreshold+1 must not suggest)", "txxx", known, got)
	}
}

// TestSuggest_TiesBreakDeterministically pins the exact winner chosen when
// two known entries are equidistant from got, so the choice does not depend
// on map iteration order (defect: suggest was fed a map-ranged vocabulary
// upstream, and even here a "<" comparison alone leaves the outcome
// dependent on slice order unless it is explicit and tested).
func TestSuggest_TiesBreakDeterministically(t *testing.T) {
	// "cat" and "cot" are both distance 1 from "cbt"; the lexicographically
	// smaller wins, regardless of which order they appear in known.
	if got := suggest("cbt", []string{"cot", "cat"}); got != "cat" {
		t.Fatalf(`suggest("cbt", [cot cat]) = %q, want "cat"`, got)
	}
	if got := suggest("cbt", []string{"cat", "cot"}); got != "cat" {
		t.Fatalf(`suggest("cbt", [cat cot]) = %q, want "cat"`, got)
	}
}
