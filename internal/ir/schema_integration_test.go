package ir

import (
	"testing"

	"github.com/cdhdt/lapigo/internal/source"
)

// TestSchema_Compose builds the spec §3 `article` example by hand — the same
// shape a resolver would produce — to prove the IR types actually compose:
// a SortKey and a Filter both point at the same *Field Article.Fields holds,
// and a Relation points at the same *Entity the Schema holds for "user".
// This is what spec §2.2 means by "an IR that still requires resolution is
// not resolved": nothing here is a name a template would have to look up.
// The result must also pass Freeze, since this is exactly the shape a
// resolver is meant to hand off.
func TestSchema_Compose(t *testing.T) {
	userID := &Field{Name: source.Bare("id"), GoName: "ID", Column: "id", Type: FieldTypeUUID, PK: true}
	user := &Entity{
		Name:   "user",
		GoName: "User",
		Table:  "users",
		Fields: []*Field{userID},
		PK:     userID,
	}

	articleID := &Field{Name: source.Bare("id"), GoName: "ID", Column: "id", Type: FieldTypeUUID, PK: true}
	status := &Field{
		Name: source.Bare("status"), GoName: "Status", Column: "status", Type: FieldTypeEnum, EnumGoType: "ArticleStatus",
		EnumValues: []EnumValue{{Name: source.Bare("draft"), GoName: "Draft"}, {Name: source.Bare("published"), GoName: "Published"}},
	}
	createdAt := &Field{Name: source.Bare("created_at"), GoName: "CreatedAt", Column: "created_at", Type: FieldTypeTimestamp, Immutable: true}

	article := &Entity{
		Name:   "article",
		GoName: "Article",
		Table:  "articles",
		Fields: []*Field{articleID, status, createdAt},
		PK:     articleID,
		Sort: SortSpec{
			Desc: true,
			Keys: []SortKey{
				{Field: createdAt},
				{Field: articleID}, // the unique tiebreaker
			},
		},
		Filters: []Filter{
			{Field: status, Op: FilterOpEq},
		},
		Relations: []Relation{
			{
				Name: "author", GoName: "Author", Target: user, Column: "author_id", GoType: user.PK.GoType(), OnDelete: "RESTRICT",
				TargetSpan: source.Span{Start: source.Pos{Line: 1, Column: 1}, End: source.Pos{Line: 1, Column: 5}},
			},
		},
		Endpoints: []Endpoint{
			{Kind: EndpointList},
			{Kind: EndpointGet},
		},
	}

	schema := &Schema{Entities: []*Entity{article, user}} // already sorted: "article" < "user"
	if err := schema.Freeze(); err != nil {
		t.Fatalf("Freeze() = %v, want nil: this is the shape a resolver is meant to produce", err)
	}

	// A template resolving the last sort key never touches a name.
	lastKey := article.Sort.Keys[len(article.Sort.Keys)-1]
	if !lastKey.Field.PK {
		t.Error("last sort key does not resolve to the PK field directly")
	}
	if reason := lastKey.Field.IsSortEligible(); reason != "" {
		t.Errorf("PK field ineligible as sort key: %s", reason)
	}

	// The filter's field is the same Field the entity declared, not a copy.
	if article.Filters[0].Field != article.Fields[1] {
		t.Error("Filter.Field is not the same pointer as the entity's declared field")
	}

	// The relation resolves to the actual target entity, by pointer
	// identity, reachable by name too.
	got := article.Relations[0].Target
	if got != user {
		t.Error("Relation.Target is not the same *Entity as user")
	}
	if got.Name != "user" {
		t.Errorf("Relation.Target.Name = %q, want %q", got.Name, "user")
	}

	if dir := article.Sort.Direction(); dir != "DESC" {
		t.Errorf("Sort.Direction() = %q, want %q", dir, "DESC")
	}

	if got, want := status.GoType(), "ArticleStatus"; got != want {
		t.Errorf("status field GoType() = %q, want %q", got, want)
	}
}
