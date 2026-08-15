package ir

import "testing"

func TestFilterOp_String(t *testing.T) {
	if got, want := FilterOpEq.String(), "eq"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// TestFilterOp_String_Unknown asserts the exact placeholder. "eq" is itself
// non-empty, so a got == "" assertion would not catch FilterOp.String()
// wrongly reporting an out-of-range value as equality.
func TestFilterOp_String_Unknown(t *testing.T) {
	op := FilterOp(99)
	const want = "FilterOp(99)"
	if got := op.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
