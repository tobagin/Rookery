package gitstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/rookerylabs/rookery/internal/rhost/rhosttest"
)

// TestRemoteStore drives the same git operations through the ssh shim: the
// "remote host" is this machine, so this exercises the full quoting path.
func TestRemoteStore(t *testing.T) {
	rhosttest.InstallShim(t)
	ctx := context.Background()
	dir := t.TempDir()

	if _, err := OpenRemote(ctx, "fake", dir); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("OpenRemote(plain dir) = %v, want ErrNotRepo (never git-init a remote host)", err)
	}

	local, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.container"), []byte("[Container]\nImage=a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := local.CommitFile(ctx, "x.container", "init"); err != nil {
		t.Fatal(err)
	}

	remote, err := OpenRemote(ctx, "fake", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.container"), []byte("[Container]\nImage=b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := remote.CommitFile(ctx, "x.container", "update over ssh"); err != nil {
		t.Fatal(err)
	}
	hist, err := remote.History(ctx, "x.container", 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("History = %v, %v; want 2 commits", hist, err)
	}
	if hist[0].Subject != "update over ssh" {
		t.Errorf("newest subject = %q", hist[0].Subject)
	}
	content, err := remote.Show(ctx, hist[1].Hash, "x.container")
	if err != nil || content != "[Container]\nImage=a\n" {
		t.Errorf("Show(first) = %q, %v", content, err)
	}
}

func TestOpenPlainDirWithoutCreate(t *testing.T) {
	if _, err := Open(t.TempDir(), false); !errors.Is(err, ErrNotRepo) {
		t.Errorf("Open(plain dir, create=false) = %v, want ErrNotRepo", err)
	}
}

func TestCommitHistoryShowRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// A file with no commits yet has empty history, not an error.
	hist, err := s.History(ctx, "app.container", 10)
	if err != nil || len(hist) != 0 {
		t.Fatalf("empty-repo history = %v, %v", hist, err)
	}

	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "app.container"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("[Container]\nImage=a\n")
	if err := s.CommitFile(ctx, "app.container", "rookery: save app.container"); err != nil {
		t.Fatal(err)
	}
	// Committing again with no changes is a silent no-op.
	if err := s.CommitFile(ctx, "app.container", "noop"); err != nil {
		t.Fatalf("no-op commit: %v", err)
	}
	write("[Container]\nImage=b\n")
	if err := s.CommitFile(ctx, "app.container", "rookery: save app.container (2)"); err != nil {
		t.Fatal(err)
	}

	hist, err = s.History(ctx, "app.container", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 2 {
		t.Fatalf("history has %d entries, want 2: %v", len(hist), hist)
	}
	if hist[0].Subject != "rookery: save app.container (2)" || hist[0].Time == 0 {
		t.Errorf("hist[0] = %+v", hist[0])
	}

	old, err := s.Show(ctx, hist[1].Hash, "app.container")
	if err != nil {
		t.Fatal(err)
	}
	if old != "[Container]\nImage=a\n" {
		t.Errorf("Show(first commit) = %q", old)
	}

	// Deletion is also tracked.
	if err := os.Remove(filepath.Join(dir, "app.container")); err != nil {
		t.Fatal(err)
	}
	if err := s.CommitFile(ctx, "app.container", "rookery: delete app.container"); err != nil {
		t.Fatal(err)
	}
	hist, _ = s.History(ctx, "app.container", 10)
	if len(hist) != 3 {
		t.Errorf("history after delete has %d entries, want 3", len(hist))
	}
}

func TestShowRejectsBadHash(t *testing.T) {
	s, err := Open(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"HEAD", "main", "abc; rm -rf /", "--help"} {
		if _, err := s.Show(context.Background(), bad, "x"); err == nil {
			t.Errorf("Show(%q): want error", bad)
		}
	}
}

func TestOpenDetectsExistingRepo(t *testing.T) {
	dir := t.TempDir()
	if _, err := Open(dir, true); err != nil {
		t.Fatal(err)
	}
	// Re-open without create: the existing repo must be detected.
	if _, err := Open(dir, false); err != nil {
		t.Errorf("Open(existing repo, create=false) = %v", err)
	}
}
