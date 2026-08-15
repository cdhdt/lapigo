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
