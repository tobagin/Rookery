package appdb

import (
	"database/sql"
	"time"
)

type APIToken struct {
	IDHash     string `json:"-"`
	Name       string `json:"name"`
	Role       string `json:"role"`
	ExpiresAt  string `json:"expiresAt,omitempty"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	CreatedAt  string `json:"createdAt,omitempty"`
}

func PutAPIToken(db *sql.DB, hash, name, role string, expiresAt time.Time) error {
	exp := ""
	if !expiresAt.IsZero() {
		exp = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := db.Exec(`INSERT INTO api_tokens(id_hash, name, role, expires_at) VALUES (?, ?, ?, ?)`, hash, name, role, exp)
	return err
}

func GetAPIToken(db *sql.DB, hash string) (APIToken, bool, error) {
	var tok APIToken
	err := db.QueryRow(`SELECT id_hash, name, role, expires_at, last_used_at, created_at FROM api_tokens WHERE id_hash = ?`, hash).
		Scan(&tok.IDHash, &tok.Name, &tok.Role, &tok.ExpiresAt, &tok.LastUsedAt, &tok.CreatedAt)
	if err == sql.ErrNoRows {
		return APIToken{}, false, nil
	}
	return tok, err == nil, err
}

func TouchAPIToken(db *sql.DB, hash string) error {
	_, err := db.Exec(`UPDATE api_tokens SET last_used_at = CURRENT_TIMESTAMP WHERE id_hash = ?`, hash)
	return err
}

func ListAPITokens(db *sql.DB) ([]APIToken, error) {
	rows, err := db.Query(`SELECT id_hash, name, role, expires_at, last_used_at, created_at FROM api_tokens ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIToken
	for rows.Next() {
		var tok APIToken
		if err := rows.Scan(&tok.IDHash, &tok.Name, &tok.Role, &tok.ExpiresAt, &tok.LastUsedAt, &tok.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, tok)
	}
	return out, rows.Err()
}

func DeleteAPIToken(db *sql.DB, name string) error {
	_, err := db.Exec(`DELETE FROM api_tokens WHERE name = ?`, name)
	return err
}
