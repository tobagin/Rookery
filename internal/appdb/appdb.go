// Package appdb owns Rookery's local SQLite database schema.
package appdb

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const CurrentSchema = 4

// Open opens rookery.db and applies all schema migrations.
func Open(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil && !os.IsNotExist(err) {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
	version INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
		return err
	}
	version := 0
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return err
	}
	for version < CurrentSchema {
		next := version + 1
		if err := applyMigration(db, next); err != nil {
			return fmt.Errorf("migration %d: %w", next, err)
		}
		version = next
	}
	return nil
}

func applyMigration(db *sql.DB, version int) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	switch version {
	case 1:
		if _, err := tx.Exec(`CREATE TABLE users (
	username TEXT PRIMARY KEY,
	email TEXT NOT NULL DEFAULT '',
	role TEXT NOT NULL CHECK (role IN ('admin', 'viewer')),
	salt TEXT NOT NULL,
	hash TEXT NOT NULL,
	must_change_password INTEGER NOT NULL DEFAULT 0,
	must_set_email INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE UNIQUE INDEX users_email_unique ON users(lower(email)) WHERE email <> ''`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE TABLE settings (
	key TEXT PRIMARY KEY,
	value_json TEXT NOT NULL,
	source TEXT NOT NULL DEFAULT 'db',
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE TABLE setting_overrides (
	key TEXT PRIMARY KEY,
	value_json TEXT NOT NULL,
	source TEXT NOT NULL,
	locked INTEGER NOT NULL DEFAULT 1,
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
	case 2:
		if _, err := tx.Exec(`CREATE TABLE node_metadata (
	node_id TEXT PRIMARY KEY,
	labels_json TEXT NOT NULL DEFAULT '[]',
	updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
	case 3:
		if _, err := tx.Exec(`CREATE TABLE audit_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	actor TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL,
	target TEXT NOT NULL DEFAULT '',
	detail_json TEXT NOT NULL DEFAULT '{}',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE INDEX audit_events_created_at_idx ON audit_events(created_at DESC, id DESC)`); err != nil {
			return err
		}
	case 4:
		if _, err := tx.Exec(`CREATE TABLE policy_waivers (
	key TEXT PRIMARY KEY,
	reason TEXT NOT NULL DEFAULT '',
	created_by TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
)`); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown schema version %d", version)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, version); err != nil {
		return err
	}
	return tx.Commit()
}
