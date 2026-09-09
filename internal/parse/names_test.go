package parse

import "testing"

func TestGoName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"article", "Article"},
		{"created_at", "CreatedAt"},
		{"id", "ID"},
		{"user_id", "UserID"},
		{"author", "Author"},
		{"status", "Status"},
		{"a", "A"},
		{"", ""},

		// goName is also used, unmodified, to compute an enum value's Go
		// identifier (internal/ir.EnumValue.GoName) -- unlike a field or
		// entity name, an enum value is never passed through
		// requireIdentifier first, so it may contain any character
		// buildEnumValues accepts (not just letters, digits and
		// underscore). These cases pin the separator rule that matters for
		// that caller: any run of non-alphanumeric characters is a
		// boundary, not just "_" -- see issue #24, where "in-progress" and
		// "in_progress" both had to reach the same identifier for the
		// defect to be reachable at all.
		{"in_progress", "InProgress"},
		{"in-progress", "InProgress"},
		{"in progress", "InProgress"},
		{"-", ""},
		{"---", ""},
	}
	for _, c := range cases {
		if got := goName(c.in); got != c.want {
			t.Errorf("goName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDefaultTableName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"article", "articles"},
		{"user", "users"},
	}
	for _, c := range cases {
		if got := defaultTableName(c.in); got != c.want {
			t.Errorf("defaultTableName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
