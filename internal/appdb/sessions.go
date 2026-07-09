package appdb

import (
	"database/sql"
	"time"
)

type Session struct {
	IDHash     string
	Username   string
	Role       string
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

func PutSession(db *sql.DB, idHash, username, role string, expiresAt, lastSeenAt time.Time) error {
	_, err := db.Exec(`INSERT INTO sessions(id_hash, username, role, expires_at, last_seen_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT(id_hash) DO UPDATE SET username = excluded.username, role = excluded.role, expires_at = excluded.expires_at, last_seen_at = excluded.last_seen_at`,
		idHash, username, role, expiresAt.UTC().Format(time.RFC3339Nano), lastSeenAt.UTC().Format(time.RFC3339Nano))
	return err
}

func GetSession(db *sql.DB, idHash string) (Session, bool, error) {
	var row Session
	var expires, lastSeen string
	err := db.QueryRow(`SELECT id_hash, username, role, expires_at, last_seen_at FROM sessions WHERE id_hash = ?`, idHash).
		Scan(&row.IDHash, &row.Username, &row.Role, &expires, &lastSeen)
	if err == sql.ErrNoRows {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, err
	}
	row.ExpiresAt, err = time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return Session{}, false, err
	}
	row.LastSeenAt, _ = time.Parse(time.RFC3339Nano, lastSeen)
	return row, true, nil
}

func DeleteSession(db *sql.DB, idHash string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE id_hash = ?`, idHash)
	return err
}

func DeleteUserSessions(db *sql.DB, username, exceptHash string) error {
	if exceptHash == "" {
		_, err := db.Exec(`DELETE FROM sessions WHERE username = ?`, username)
		return err
	}
	_, err := db.Exec(`DELETE FROM sessions WHERE username = ? AND id_hash <> ?`, username, exceptHash)
	return err
}

func DeleteExpiredSessions(db *sql.DB, now time.Time) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now.UTC().Format(time.RFC3339Nano))
	return err
}
