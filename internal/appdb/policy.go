package appdb

import "database/sql"

type PolicyWaiver struct {
	Key       string `json:"key"`
	Reason    string `json:"reason"`
	CreatedBy string `json:"createdBy"`
	CreatedAt string `json:"createdAt"`
}

func GetPolicyWaivers(db *sql.DB) (map[string]PolicyWaiver, error) {
	rows, err := db.Query(`SELECT key, reason, created_by, created_at FROM policy_waivers`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]PolicyWaiver{}
	for rows.Next() {
		var w PolicyWaiver
		if err := rows.Scan(&w.Key, &w.Reason, &w.CreatedBy, &w.CreatedAt); err != nil {
			return nil, err
		}
		out[w.Key] = w
	}
	return out, rows.Err()
}

func PutPolicyWaiver(db *sql.DB, key, reason, actor string) error {
	_, err := db.Exec(`INSERT INTO policy_waivers(key, reason, created_by, created_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(key) DO UPDATE SET reason = excluded.reason, created_by = excluded.created_by, created_at = CURRENT_TIMESTAMP`,
		key, reason, actor)
	return err
}

func DeletePolicyWaiver(db *sql.DB, key string) error {
	_, err := db.Exec(`DELETE FROM policy_waivers WHERE key = ?`, key)
	return err
}
