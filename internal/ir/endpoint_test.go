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

// TestEndpointKind_Method covers the Go 1.22 routing-pattern verb for every
// EndpointKind (spec §2.2, §6.6: PATCH only, PUT is never generated).
func TestEndpointKind_Method(t *testing.T) {
	tests := []struct {
		kind EndpointKind
		want string
	}{
		{EndpointList, "GET"},
		{EndpointGet, "GET"},
		{EndpointCreate, "POST"},
		{EndpointUpdate, "PATCH"},
		{EndpointDelete, "DELETE"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.kind.Method(); got != tt.want {
				t.Errorf("Method() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEndpointKind_Method_Unknown asserts the exact placeholder, matching the
// pattern of FieldType.PgType and FieldType.GoType's own out-of-range
// handling: an unusable string, not a plausible-looking real HTTP method.
func TestEndpointKind_Method_Unknown(t *testing.T) {
	k := EndpointKind(99)
	const want = "<unknown EndpointKind 99>"
	if got := k.Method(); got != want {
		t.Errorf("Method() = %q, want %q", got, want)
	}
}

// TestEntity_HasEndpointPredicates covers every per-kind predicate against
// an entity whose Endpoints deliberately omit create and delete, so that a
// predicate hardcoded to always answer true, or one that checked the wrong
// EndpointKind, would fail.
func TestEntity_HasEndpointPredicates(t *testing.T) {
	e := &Entity{
		Name: "article",
		Endpoints: []Endpoint{
			{Kind: EndpointList},
			{Kind: EndpointGet},
			{Kind: EndpointUpdate},
		},
	}

	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{"HasList", e.HasList(), true},
		{"HasGet", e.HasGet(), true},
		{"HasCreate", e.HasCreate(), false},
		{"HasUpdate", e.HasUpdate(), true},
		{"HasDelete", e.HasDelete(), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("%s() = %v, want %v", tt.name, tt.got, tt.want)
			}
		})
	}
}

// TestEntity_HasEndpointPredicates_Empty pins that every predicate answers
// false on an entity with no Endpoints at all, not merely on one that omits
// a single kind.
func TestEntity_HasEndpointPredicates_Empty(t *testing.T) {
	e := &Entity{Name: "article"}

	if e.HasList() || e.HasGet() || e.HasCreate() || e.HasUpdate() || e.HasDelete() {
		t.Errorf("all Has* predicates on an entity with no Endpoints = true, want all false")
	}
}
