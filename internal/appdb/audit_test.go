package appdb

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDeleteAuditEventsBefore(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := AddAuditEvent(db, "old", "unit.save", "a", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE audit_events SET created_at = '2000-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	if err := AddAuditEvent(db, "new", "unit.save", "b", nil); err != nil {
		t.Fatal(err)
	}
	n, err := DeleteAuditEventsBefore(db, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d events, want 1", n)
	}
	events, err := ListAuditEvents(db, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Actor != "new" {
		t.Fatalf("unexpected surviving events: %+v", events)
	}
}
