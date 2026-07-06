// Package userstore keeps Rookery's local accounts in one JSON file —
// username, role, and a PBKDF2-SHA256 password hash. It exists so the
// first-run wizard has somewhere durable to put the admin account without
// dragging in a database; the file is human-readable and lives next to the
// rest of the host's config.
package userstore

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
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

// Store is the accounts file. Safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	path  string
	users []User
}

// Open loads (or prepares to create) the store at path. A missing file is
// an empty store — that's what triggers the first-run wizard.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.users); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Empty reports whether no accounts exist yet (first run).
func (s *Store) Empty() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users) == 0
}

// List returns usernames and roles (never hashes).
func (s *Store) List() []User {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]User, len(s.users))
	for i, u := range s.users {
		out[i] = publicUser(u)
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
	for _, u := range s.users {
		if u.Name == name {
			return fmt.Errorf("user %s already exists", name)
		}
	}
	s.users = append(s.users, User{
		Name:               name,
		Email:              email,
		Role:               role,
		Salt:               hex.EncodeToString(salt),
		Hash:               hex.EncodeToString(pbkdf2(password, salt)),
		MustChangePassword: user.MustChangePassword,
		MustSetEmail:       user.MustSetEmail,
	})
	return s.save()
}

// Delete removes an account. The last admin cannot be deleted — that would
// lock everyone out of a tool whose whole job is fixing lockouts.
func (s *Store) Delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	admins, idx := 0, -1
	for i, u := range s.users {
		if u.Role == RoleAdmin {
			admins++
		}
		if u.Name == name {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("no such user %s", name)
	}
	if s.users[idx].Role == RoleAdmin && admins == 1 {
		return fmt.Errorf("cannot delete the last admin")
	}
	s.users = append(s.users[:idx], s.users[idx+1:]...)
	return s.save()
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
	for i, u := range s.users {
		if u.Name == name {
			s.users[i].Salt = hex.EncodeToString(salt)
			s.users[i].Hash = hex.EncodeToString(pbkdf2(password, salt))
			return s.save()
		}
	}
	return fmt.Errorf("no such user %s", name)
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
	var found *User
	for i := range s.users {
		if s.users[i].Name == name || (s.users[i].Email != "" && strings.EqualFold(s.users[i].Email, name)) {
			found = &s.users[i]
		}
	}
	s.mu.Unlock()
	if found == nil {
		pbkdf2(password, make([]byte, 16))
		return User{}, false
	}
	salt, err := hex.DecodeString(found.Salt)
	if err != nil {
		return User{}, false
	}
	want, err := hex.DecodeString(found.Hash)
	if err != nil {
		return User{}, false
	}
	if subtle.ConstantTimeCompare(pbkdf2(password, salt), want) != 1 {
		return User{}, false
	}
	return publicUser(*found), true
}

// Get returns public account metadata by username.
func (s *Store) Get(name string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.Name == name {
			return publicUser(u), true
		}
	}
	return User{}, false
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
	for i, u := range s.users {
		if u.Name == name {
			s.users[i].Email = email
			s.users[i].Salt = hex.EncodeToString(salt)
			s.users[i].Hash = hex.EncodeToString(pbkdf2(newPassword, salt))
			s.users[i].MustChangePassword = false
			s.users[i].MustSetEmail = false
			return s.save()
		}
	}
	return fmt.Errorf("no such user %s", name)
}

// Fingerprint digests every account's name, salt, and hash. It keys the
// share-link HMAC: changing any password (or the account list) changes the
// fingerprint and so revokes every outstanding link.
func (s *Store) Fingerprint() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	h := sha256.New()
	for _, u := range s.users {
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

// save writes the file atomically with owner-only permissions. Caller holds
// the lock.
func (s *Store) save() error {
	data, err := json.MarshalIndent(s.users, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
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
