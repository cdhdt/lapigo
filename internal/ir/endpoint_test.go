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

func TestEndpointKind_String_Unknown(t *testing.T) {
	var k EndpointKind = 999
	if got := k.String(); got == "" {
		t.Error("String() on an unknown EndpointKind returned empty string, want a diagnostic placeholder")
	}
}
