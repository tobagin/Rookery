package userstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.json")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Empty() {
		t.Fatal("fresh store must be empty (drives the first-run wizard)")
	}

	if err := s.Create("admin", "hunter2hunter2", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateWithProfile(User{
		Name:               "bootstrap",
		Email:              "admin@example.com",
		Role:               RoleAdmin,
		MustChangePassword: true,
		MustSetEmail:       true,
	}, "temporarypass"); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("admin", "otherpass1", RoleAdmin); err == nil {
		t.Error("duplicate username accepted")
	}
	if err := s.Create("couch", "viewerpass", RoleViewer); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct{ name, pass, role string }{
		{"", "longenough", RoleAdmin},
		{"x y", "longenough", RoleAdmin},
		{"ok", "short", RoleAdmin},
		{"ok", "longenough", "root"},
	} {
		if err := s.Create(bad.name, bad.pass, bad.role); err == nil {
			t.Errorf("Create(%q,%q,%q): want error", bad.name, bad.pass, bad.role)
		}
	}

	// Reload from disk: hashes survive, verification works.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if role, ok := s2.Verify("admin", "hunter2hunter2"); !ok || role != RoleAdmin {
		t.Errorf("Verify(admin) = %q, %v", role, ok)
	}
	if u, ok := s2.VerifyUser("admin@example.com", "temporarypass"); !ok || u.Name != "bootstrap" || !u.MustChangePassword || !u.MustSetEmail {
		t.Errorf("VerifyUser(email) = %#v, %v", u, ok)
	}
	if !s2.NeedsOnboarding("bootstrap") {
		t.Error("bootstrap user should need onboarding")
	}
	if err := s2.CompleteOnboarding("bootstrap", "owner@example.test", "temporarypass", "newtemporarypass"); err != nil {
		t.Fatal(err)
	}
	if s2.NeedsOnboarding("bootstrap") {
		t.Error("bootstrap user still needs onboarding after completion")
	}
	if u, ok := s2.VerifyUser("owner@example.test", "newtemporarypass"); !ok || u.Email != "owner@example.test" {
		t.Errorf("updated email login failed: %#v %v", u, ok)
	}
	if u, ok := s2.GetByEmail("OWNER@example.test"); !ok || u.Name != "bootstrap" || u.Role != RoleAdmin {
		t.Errorf("GetByEmail = %#v, %v", u, ok)
	}
	if _, ok := s2.Verify("admin", "wrongpass"); ok {
		t.Error("wrong password verified")
	}
	if _, ok := s2.Verify("ghost", "hunter2hunter2"); ok {
		t.Error("unknown user verified")
	}

	// Database must be private and never contain a plaintext password.
	dbPath := filepath.Join(filepath.Dir(path), "rookery.db")
	st, _ := os.Stat(dbPath)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("database mode = %v, want 0600", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(dbPath)
	if strings.Contains(string(raw), "hunter2hunter2") {
		t.Error("plaintext password on disk")
	}

	if err := s2.SetPassword("couch", "newviewerpass"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.Verify("couch", "viewerpass"); ok {
		t.Error("old password still valid after change")
	}
	if role, ok := s2.Verify("couch", "newviewerpass"); !ok || role != RoleViewer {
		t.Error("new password rejected")
	}

	// One of multiple admins is deletable; the last admin is not.
	if err := s2.Delete("bootstrap"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete("admin"); err == nil {
		t.Error("deleted the last admin")
	}
	if err := s2.Delete("couch"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete("ghost"); err == nil {
		t.Error("deleted a ghost")
	}
}
