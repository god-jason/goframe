package api

import "testing"

func TestPageQueryNormalize(t *testing.T) {
	query := PageQuery{Page: 0, PageSize: 1000, Order: "unknown"}
	query.Normalize()
	if query.Page != 1 {
		t.Fatalf("page = %d, want 1", query.Page)
	}
	if query.PageSize != MaxPageSize {
		t.Fatalf("page size = %d, want %d", query.PageSize, MaxPageSize)
	}
	if query.Order != "desc" {
		t.Fatalf("order = %q, want desc", query.Order)
	}
	if query.Offset() != 0 {
		t.Fatalf("offset = %d, want 0", query.Offset())
	}
}

func TestSortClauseUsesWhitelist(t *testing.T) {
	query := PageQuery{Sort: "created_at", Order: "asc"}
	query.Normalize()
	column, direction := query.SortClause(map[string]string{"created_at": "created_at", "name": "username"}, "name")
	if column != "created_at" || direction != "asc" {
		t.Fatalf("got (%q, %q), want (created_at, asc)", column, direction)
	}

	query.Sort = "id; drop table users"
	column, _ = query.SortClause(map[string]string{"name": "username"}, "name")
	if column != "username" {
		t.Fatalf("unexpected sort column %q, want fallback username", column)
	}
}
