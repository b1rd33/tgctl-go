package store

import "testing"

func TestExportMessagesOrdersAndFilters(t *testing.T) {
	db := setupMessages(t)
	rows, err := ExportMessages(db, ExportOptions{ChatID: 1, Since: "2026-05-02", Until: "2026-05-04T23:59:59", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].MessageID != 11 || rows[1].MessageID != 12 {
		t.Fatalf("rows=%+v", rows)
	}
	rows, err = ExportMessages(db, ExportOptions{ChatID: 1, IncludeDeleted: true})
	if err != nil || len(rows) != 5 || rows[len(rows)-1].MessageID != 14 {
		t.Fatalf("deleted export rows=%d err=%v", len(rows), err)
	}
}
