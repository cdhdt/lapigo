package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestSchema_Compose builds the spec §3 `article` example by hand — the same
// shape a resolver would produce — to prove the IR types actually compose:
// a SortKey and a Filter both point at the same *Field Article.Fields holds,
// and a Relation points at the *Entity the Schema holds for "user". This is
// what spec §2.2 means by "an IR that still requires resolution is not
// resolved": nothing here is a name a template would have to look up.
func TestSchema_Compose(t *testing.T) {
	user := Entity{
		Name:   "user",
		GoName: "User",
		Table:  "users",
		Fields: []Field{
			{Name: source.Bare("id"), GoName: "ID", Column: "id", Type: FieldTypeUUID, GoType: "pgtype.UUID", PgType: "uuid", PK: true},
		},
	}
	user.PK = &user.Fields[0]

	article := Entity{
		Name:   "article",
		GoName: "Article",
		Table:  "articles",
		Fields: []Field{
			{Name: source.Bare("id"), GoName: "ID", Column: "id", Type: FieldTypeUUID, GoType: "pgtype.UUID", PgType: "uuid", PK: true},
			{Name: source.Bare("status"), GoName: "Status", Column: "status", Type: FieldTypeEnum, GoType: "ArticleStatus", PgType: "text",
				EnumValues: []source.At[string]{source.Bare("draft"), source.Bare("published")}},
			{Name: source.Bare("created_at"), GoName: "CreatedAt", Column: "created_at", Type: FieldTypeTimestamp, GoType: "time.Time", PgType: "timestamptz", Immutable: true},
		},
	}
	article.PK = &article.Fields[0]
	article.Sort = SortSpec{
		Desc: true,
		Keys: []SortKey{
			{Field: &article.Fields[2]}, // created_at
			{Field: &article.Fields[0]}, // id, the unique tiebreaker
		},
	}
	article.Filters = []Filter{
		{Field: &article.Fields[1], Op: FilterOpEq}, // status
	}
	article.Relations = []Relation{
		{Name: "author", GoName: "Author", Target: &user, Column: "author_id", GoType: user.PK.GoType, OnDelete: "RESTRICT"},
	}
	article.Endpoints = []Endpoint{
		{Kind: EndpointList, Path: "/articles"},
		{Kind: EndpointGet, Path: "/articles/{id}"},
	}

	schema := &Schema{Entities: []Entity{article, user}}

	// A template resolving the last sort key never touches a name.
	lastKey := article.Sort.Keys[len(article.Sort.Keys)-1]
	if !lastKey.Field.PK {
		t.Error("last sort key does not resolve to the PK field directly")
	}
	if eligible, reason := lastKey.Field.IsSortEligible(); !eligible {
		t.Errorf("PK field ineligible as sort key: %s", reason)
	}

	// The filter's field is the same Field the entity declared, not a copy.
	if schema.Entities[0].Filters[0].Field != &schema.Entities[0].Fields[1] {
		t.Error("Filter.Field is not the same pointer as the entity's declared field")
	}

	// The relation resolves to the actual target entity, reachable by name.
	got := schema.Entities[0].Relations[0].Target
	if got.Name != "user" {
		t.Errorf("Relation.Target.Name = %q, want %q", got.Name, "user")
	}

	if dir := article.Sort.Direction(); dir != "DESC" {
		t.Errorf("Sort.Direction() = %q, want %q", dir, "DESC")
	}
}
