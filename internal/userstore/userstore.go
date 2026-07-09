// Package userstore keeps Rookery's local accounts in SQLite: username,
// role, and a PBKDF2-SHA256 password hash. Open still accepts the old
// users.json path so existing callers migrate to sibling rookery.db.
package userstore

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/rookerylabs/rookery/internal/appdb"
)

const (
	// OWASP's 2023+ recommendation for PBKDF2-HMAC-SHA256.
	iterations = 210_000
	keyLen     = 32

	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

var (
	nameRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,127}$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// User is one stored account. The password never appears in memory or on
// disk — only its salted hash.
type User struct {
	Name               string `json:"name"`
	Email              string `json:"email,omitempty"`
	Role               string `json:"role"`
	Salt               string `json:"salt,omitempty"`
	Hash               string `json:"hash,omitempty"`
	MustChangePassword bool   `json:"mustChangePassword,omitempty"`
	MustSetEmail       bool   `json:"mustSetEmail,omitempty"`
}

// Store is the accounts database. Safe for concurrent use.
type Store struct {
	mu         sync.Mutex
	db         *sql.DB
	path       string
	legacyPath string
}

// DB exposes the shared app database for settings and future admin metadata.
func (s *Store) DB() *sql.DB { return s.db }

// Path returns the effective SQLite database path.
func (s *Store) Path() string { return s.path }

// Open loads the SQLite store. If path names users.json, rookery.db in the
// same directory is used and users.json is migrated only when the DB has no
// users. The JSON file is left untouched as a rollback artifact.
func Open(path string) (*Store, error) {
	dbPath := path
	legacyPath := ""
	if filepath.Base(path) == "users.json" {
		dbPath = filepath.Join(filepath.Dir(path), "rookery.db")
		legacyPath = path
	}
	db, err := appdb.Open(dbPath)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, path: dbPath, legacyPath: legacyPath}
	if legacyPath != "" {
		if err := s.migrateLegacyJSON(); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

// Empty reports whether no accounts exist yet (first run).
func (s *Store) Empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return true
	}
	return n == 0
}

// List returns usernames and roles (never hashes).
func (s *Store) List() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT username, email, role, must_change_password, must_set_email FROM users ORDER BY username`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Name, &u.Email, &u.Role, &u.MustChangePassword, &u.MustSetEmail); err == nil {
			out = append(out, u)
		}
	}
	return out
}

// Create adds an account and persists the file.
func (s *Store) Create(name, password, role string) error {
	return s.CreateWithProfile(User{Name: name, Role: role}, password)
}

// CreateWithProfile adds an account with profile/onboarding flags and
// persists the file.
func (s *Store) CreateWithProfile(user User, password string) error {
	name := strings.TrimSpace(user.Name)
	if !nameRe.MatchString(name) {
		return fmt.Errorf("invalid username")
	}
	email := strings.TrimSpace(user.Email)
	if email != "" && !emailRe.MatchString(email) {
		return fmt.Errorf("invalid email")
	}
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	role := user.Role
	if role != RoleAdmin && role != RoleViewer {
		return fmt.Errorf("role must be %s or %s", RoleAdmin, RoleViewer)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`INSERT INTO users(username, email, role, salt, hash, must_change_password, must_set_email)
VALUES (?, ?, ?, ?, ?, ?, ?)`, name, email, role, hex.EncodeToString(salt), hex.EncodeToString(pbkdf2(password, salt)), user.MustChangePassword, user.MustSetEmail)
	if err != nil {
		if strings.Contains(err.Error(), "constraint failed") {
			return fmt.Errorf("user %s already exists", name)
		}
		return err
	}
	return nil
}

// Delete removes an account. The last admin cannot be deleted — that would
// lock everyone out of a tool whose whole job is fixing lockouts.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE username = ?`, name).Scan(&role); err == sql.ErrNoRows {
		return fmt.Errorf("no such user %s", name)
	} else if err != nil {
		return err
	}
	if role == RoleAdmin {
		admins, err := s.adminCount()
		if err != nil {
			return err
		}
		if admins == 1 {
			return fmt.Errorf("cannot delete the last admin")
		}
	}
	res, err := s.db.Exec(`DELETE FROM users WHERE username = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no such user %s", name)
	}
	return nil
}

// SetPassword replaces name's password.
func (s *Store) SetPassword(name, password string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE users SET salt = ?, hash = ?, must_change_password = 0, updated_at = CURRENT_TIMESTAMP WHERE username = ?`,
		hex.EncodeToString(salt), hex.EncodeToString(pbkdf2(password, salt)), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no such user %s", name)
	}
	return nil
}

// Update edits account profile fields. The last admin cannot be demoted.
func (s *Store) Update(name, email, role string) error {
	email = strings.TrimSpace(email)
	if email != "" && !emailRe.MatchString(email) {
		return fmt.Errorf("invalid email")
	}
	if role != RoleAdmin && role != RoleViewer {
		return fmt.Errorf("role must be %s or %s", RoleAdmin, RoleViewer)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var oldRole string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE username = ?`, name).Scan(&oldRole); err == sql.ErrNoRows {
		return fmt.Errorf("no such user %s", name)
	} else if err != nil {
		return err
	}
	if oldRole == RoleAdmin && role == RoleViewer {
		admins, err := s.adminCount()
		if err != nil {
			return err
		}
		if admins == 1 {
			return fmt.Errorf("cannot demote the last admin")
		}
	}
	_, err := s.db.Exec(`UPDATE users SET email = ?, role = ?, updated_at = CURRENT_TIMESTAMP WHERE username = ?`, email, role, name)
	return err
}

// Verify checks credentials and returns the role. It runs the KDF even for
// unknown users so a login attempt can't probe which names exist by timing.
func (s *Store) Verify(name, password string) (role string, ok bool) {
	u, ok := s.VerifyUser(name, password)
	if !ok {
		return "", false
	}
	return u.Role, true
}

// VerifyUser checks credentials and returns public account metadata. Login
// accepts either the username or the stored email address.
func (s *Store) VerifyUser(name, password string) (User, bool) {
	s.mu.Lock()
	u, err := s.getPrivateLocked(name)
	s.mu.Unlock()
	if err != nil {
		pbkdf2(password, make([]byte, 16))
		return User{}, false
	}
	salt, err := hex.DecodeString(u.Salt)
	if err != nil {
		return User{}, false
	}
	want, err := hex.DecodeString(u.Hash)
	if err != nil {
		return User{}, false
	}
	if subtle.ConstantTimeCompare(pbkdf2(password, salt), want) != 1 {
		return User{}, false
	}
	return publicUser(u), true
}

// Get returns public account metadata by username.
func (s *Store) Get(name string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, err := s.getPublicLocked(name)
	if err != nil {
		return User{}, false
	}
	return u, true
}

// GetByEmail returns public account metadata by email address.
func (s *Store) GetByEmail(email string) (User, bool) {
	email = strings.TrimSpace(email)
	if email == "" {
		return User{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var u User
	err := s.db.QueryRow(`SELECT username, email, role, must_change_password, must_set_email FROM users WHERE email <> '' AND lower(email) = lower(?)`, email).
		Scan(&u.Name, &u.Email, &u.Role, &u.MustChangePassword, &u.MustSetEmail)
	if err != nil {
		return User{}, false
	}
	return u, true
}

// NeedsOnboarding reports whether a local user must complete first-login
// profile setup before normal API access.
func (s *Store) NeedsOnboarding(name string) bool {
	u, ok := s.Get(name)
	return ok && (u.MustChangePassword || u.MustSetEmail)
}

// CompleteOnboarding updates email and password, clearing first-login flags.
func (s *Store) CompleteOnboarding(name, email, currentPassword, newPassword string) error {
	email = strings.TrimSpace(email)
	if !emailRe.MatchString(email) {
		return fmt.Errorf("valid email is required")
	}
	if len(newPassword) < 8 {
		return fmt.Errorf("new password must be at least 8 characters")
	}
	if _, ok := s.VerifyUser(name, currentPassword); !ok {
		return fmt.Errorf("current password is incorrect")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE users SET email = ?, salt = ?, hash = ?, must_change_password = 0, must_set_email = 0, updated_at = CURRENT_TIMESTAMP WHERE username = ?`,
		email, hex.EncodeToString(salt), hex.EncodeToString(pbkdf2(newPassword, salt)), name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("no such user %s", name)
	}
	return nil
}

// Fingerprint digests every account's name, salt, and hash. It keys the
// share-link HMAC: changing any password (or the account list) changes the
// fingerprint and so revokes every outstanding link.
func (s *Store) Fingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.New()
	rows, err := s.db.Query(`SELECT username, role, salt, hash FROM users ORDER BY username`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.Name, &u.Role, &u.Salt, &u.Hash); err != nil {
			return ""
		}
		fmt.Fprintf(h, "%s:%s:%s:%s\n", u.Name, u.Role, u.Salt, u.Hash)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func publicUser(u User) User {
	return User{
		Name:               u.Name,
		Email:              u.Email,
		Role:               u.Role,
		MustChangePassword: u.MustChangePassword,
		MustSetEmail:       u.MustSetEmail,
	}
}

func (s *Store) adminCount() (int, error) {
	var admins int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&admins)
	return admins, err
}

func (s *Store) getPublicLocked(name string) (User, error) {
	var u User
	err := s.db.QueryRow(`SELECT username, email, role, must_change_password, must_set_email FROM users WHERE username = ?`, name).
		Scan(&u.Name, &u.Email, &u.Role, &u.MustChangePassword, &u.MustSetEmail)
	return u, err
}

func (s *Store) getPrivateLocked(name string) (User, error) {
	var u User
	err := s.db.QueryRow(`SELECT username, email, role, salt, hash, must_change_password, must_set_email
FROM users WHERE username = ? OR (email <> '' AND lower(email) = lower(?))`, name, name).
		Scan(&u.Name, &u.Email, &u.Role, &u.Salt, &u.Hash, &u.MustChangePassword, &u.MustSetEmail)
	return u, err
}

func (s *Store) migrateLegacyJSON() error {
	if !s.Empty() {
		return nil
	}
	data, err := os.ReadFile(s.legacyPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var users []User
	if err := json.Unmarshal(data, &users); err != nil {
		return fmt.Errorf("%s: %w", s.legacyPath, err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, u := range users {
		if _, err := tx.Exec(`INSERT INTO users(username, email, role, salt, hash, must_change_password, must_set_email)
VALUES (?, ?, ?, ?, ?, ?, ?)`, u.Name, strings.TrimSpace(u.Email), u.Role, u.Salt, u.Hash, u.MustChangePassword, u.MustSetEmail); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// pbkdf2 is RFC 2898 PBKDF2-HMAC-SHA256 — implemented here because the
// standard library has no KDF and Rookery has a zero-dependency rule.
func pbkdf2(password string, salt []byte) []byte {
	prf := hmac.New(sha256.New, []byte(password))
	var out []byte
	var block [4]byte
	for i := 1; len(out) < keyLen; i++ {
		binary.BigEndian.PutUint32(block[:], uint32(i))
		prf.Reset()
		prf.Write(salt)
		prf.Write(block[:])
		u := prf.Sum(nil)
		t := make([]byte, len(u))
		copy(t, u)
		for j := 1; j < iterations; j++ {
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(nil)
			for k := range t {
				t[k] ^= u[k]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}
