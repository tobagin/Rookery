package appdb

import (
	"database/sql"
	"encoding/json"
	"sort"
	"strings"
)

type NodeMetadata struct {
	ID     string   `json:"id"`
	Labels []string `json:"labels"`
}

func GetNodeMetadata(db *sql.DB) (map[string]NodeMetadata, error) {
	rows, err := db.Query(`SELECT node_id, labels_json FROM node_metadata`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]NodeMetadata{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var labels []string
		if err := json.Unmarshal([]byte(raw), &labels); err != nil {
			labels = nil
		}
		out[id] = NodeMetadata{ID: id, Labels: normalizeLabels(labels)}
	}
	return out, rows.Err()
}

func PutNodeLabels(db *sql.DB, id string, labels []string) error {
	data, err := json.Marshal(normalizeLabels(labels))
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO node_metadata(node_id, labels_json, updated_at) VALUES (?, ?, CURRENT_TIMESTAMP)
ON CONFLICT(node_id) DO UPDATE SET labels_json = excluded.labels_json, updated_at = CURRENT_TIMESTAMP`, id, string(data))
	return err
}

func normalizeLabels(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, label := range in {
		label = strings.TrimSpace(strings.ToLower(label))
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		out = append(out, label)
	}
	sort.Strings(out)
	return out
}
