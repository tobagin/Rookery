package rhost_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/rookerylabs/rookery/internal/rhost"
	"github.com/rookerylabs/rookery/internal/rhost/rhosttest"
)

func InstallShim(t *testing.T) { rhosttest.InstallShim(t) }

func TestQuote(t *testing.T) {
	cases := map[string]string{
		"plain":       "'plain'",
		"with space":  "'with space'",
		"it's":        `'it'\''s'`,
		"$HOME; rm x": `'$HOME; rm x'`,
	}
	for in, want := range cases {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestRunQuotingSurvivesShell(t *testing.T) {
	InstallShim(t)
	// A hostile-looking argument must arrive as one literal token.
	out, err := Run(context.Background(), "fake", QuoteArgv([]string{"printf", "%s", "a b'c$d"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out != "a b'c$d" {
		t.Errorf("round-tripped arg = %q", out)
	}
}

func TestFileOps(t *testing.T) {
	InstallShim(t)
	ctx := context.Background()
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "it's a dir", "app.container")
	content := []byte("[Container]\nImage=x\n# no trailing newline")

	if err := WriteFileAtomic(ctx, "fake", p, content); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(ctx, "fake", p)
	if err != nil || string(got) != string(content) {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	ok, err := Exists(ctx, "fake", p)
	if err != nil || !ok {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	ok, err = Exists(ctx, "fake", p+".nope")
	if err != nil || ok {
		t.Fatalf("Exists(missing) = %v, %v", ok, err)
	}
	if err := Remove(ctx, "fake", p); err != nil {
		t.Fatal(err)
	}
	if ok, _ := Exists(ctx, "fake", p); ok {
		t.Error("file still exists after Remove")
	}
	// No leftover temp files from the atomic write.
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 0 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestReadDirFiles(t *testing.T) {
	InstallShim(t)
	ctx := context.Background()
	dir := t.TempDir()
	files := map[string]string{
		"a.container": "[Container]\nImage=a\n",
		"b.pod":       "[Pod]\n# ends without newline",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := ReadDirFiles(ctx, "fake", dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d files (%v), want 2", len(got), SortedNames(got))
	}
	for name, want := range files {
		if string(got[filepath.Join(dir, name)]) != want {
			t.Errorf("content of %s = %q, want %q", name, got[filepath.Join(dir, name)], want)
		}
	}

	// Missing directory: empty result, no error.
	got, err = ReadDirFiles(ctx, "fake", filepath.Join(dir, "missing"))
	if err != nil || len(got) != 0 {
		t.Errorf("missing dir = %v, %v", got, err)
	}
}

func TestProbe(t *testing.T) {
	InstallShim(t)
	uid, home, user, err := Probe(context.Background(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	if uid != os.Getuid() || home == "" || user == "" {
		t.Errorf("Probe = %d %q %q", uid, home, user)
	}
}

func TestValidateRemoteNoGenerator(t *testing.T) {
	InstallShim(t)
	// This machine has no quadlet generator, so validation must degrade to
	// unavailable — after successfully shipping the file over.
	res, err := ValidateRemote(context.Background(), "fake", false, "x.container", "[Container]\nImage=x\n")
	if err != nil {
		t.Fatal(err)
	}
	if res.Available || !res.Valid {
		t.Errorf("result = %+v; want unavailable+valid", res)
	}
}

func TestTransportError(t *testing.T) {
	// Exit 255 (what real ssh returns when it can't connect) must surface
	// as a transport error, not a command verdict.
	dir := t.TempDir()
	shim := filepath.Join(dir, "downssh")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\necho 'connection refused' >&2\nexit 255\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := SSHCommand
	SSHCommand = []string{shim}
	defer func() { SSHCommand = old }()

	_, err := Run(context.Background(), "down", "true", nil)
	rerr, ok := err.(*Error)
	if !ok || !rerr.Transport() {
		t.Fatalf("err = %v; want transport *Error", err)
	}
	if _, err := ValidateRemote(context.Background(), "down", false, "x.container", ""); err == nil {
		t.Error("ValidateRemote over dead transport: want error, got verdict")
	}
}
