package ir

import "testing"

func TestEndpointKind_String(t *testing.T) {
	tests := []struct {
		kind EndpointKind
		want string
	}{
		{EndpointList, "list"},
		{EndpointGet, "get"},
		{EndpointCreate, "create"},
		{EndpointUpdate, "update"},
		{EndpointDelete, "delete"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.kind.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEndpointKind_String_Unknown asserts the exact placeholder. "list" is
// itself non-empty, so a got == "" assertion would not catch
// EndpointKind.String() wrongly reporting an out-of-range value as "list".
func TestEndpointKind_String_Unknown(t *testing.T) {
	k := EndpointKind(99)
	const want = "EndpointKind(99)"
	if got := k.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
