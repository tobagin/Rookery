package appdb

import (
	"database/sql"
	"encoding/json"
	"time"
)

type AuditEvent struct {
	ID        int64           `json:"id"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Target    string          `json:"target"`
	Detail    json.RawMessage `json:"detail"`
	CreatedAt time.Time       `json:"createdAt,omitempty"`
}

func AddAuditEvent(db *sql.DB, actor, action, target string, detail any) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO audit_events(actor, action, target, detail_json) VALUES (?, ?, ?, ?)`, actor, action, target, string(data))
	return err
}

func ListAuditEvents(db *sql.DB, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := db.Query(`SELECT id, actor, action, target, detail_json, created_at FROM audit_events ORDER BY datetime(created_at) DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var ev AuditEvent
		var detail, created string
		if err := rows.Scan(&ev.ID, &ev.Actor, &ev.Action, &ev.Target, &detail, &created); err != nil {
			return nil, err
		}
		ev.Detail = json.RawMessage(detail)
		if t, err := time.Parse("2006-01-02 15:04:05", created); err == nil {
			ev.CreatedAt = t
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// DeleteAuditEventsBefore prunes audit events older than cutoff and returns
// how many rows were removed.
func DeleteAuditEventsBefore(db *sql.DB, cutoff time.Time) (int64, error) {
	res, err := db.Exec(`DELETE FROM audit_events WHERE datetime(created_at) < datetime(?)`,
		cutoff.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
