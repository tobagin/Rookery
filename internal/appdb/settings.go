package appdb

import (
	"database/sql"
	"encoding/json"
	"time"
)

type Setting struct {
	Key       string          `json:"key"`
	Value     json.RawMessage `json:"value"`
	Source    string          `json:"source"`
	Locked    bool            `json:"locked"`
	UpdatedAt time.Time       `json:"updatedAt,omitempty"`
}

func GetSettings(db *sql.DB) ([]Setting, error) {
	rows, err := db.Query(`SELECT key, value_json, source, 0, updated_at FROM settings
UNION ALL
SELECT key, value_json, source, locked, updated_at FROM setting_overrides
ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Setting
	for rows.Next() {
		var s Setting
		var value, updated string
		if err := rows.Scan(&s.Key, &value, &s.Source, &s.Locked, &updated); err != nil {
			return nil, err
		}
		s.Value = json.RawMessage(value)
		if t, err := time.Parse("2006-01-02 15:04:05", updated); err == nil {
			s.UpdatedAt = t
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func PutSetting(db *sql.DB, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO settings(key, value_json, source, updated_at) VALUES (?, ?, 'db', CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, source = 'db', updated_at = CURRENT_TIMESTAMP`, key, string(data))
	return err
}

func PutOverride(db *sql.DB, key string, value any, source string, locked bool) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO setting_overrides(key, value_json, source, locked, updated_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json, source = excluded.source, locked = excluded.locked, updated_at = CURRENT_TIMESTAMP`,
		key, string(data), source, locked)
	return err
}
