package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookerylabs/rookery/internal/gitstore"
	"github.com/rookerylabs/rookery/internal/systemd"
)

// newGitServer builds a server whose system area tracks history in a real
// git repository.
func newGitServer(t *testing.T) (*Server, *fakeSystemd, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := gitstore.Open(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	sysd := &fakeSystemd{states: map[string]systemd.UnitStatus{}}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{dir}, Git: store}},
		Systemd:  sysd,
		Validate: okValidator,
	})
	return srv, sysd, dir
}

func TestHistoryDisabled(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator) // no git store attached
	rec, body := doJSON(t, srv, "GET", "/api/units/system/jellyfin.container/history", "")
	if rec.Code != http.StatusOK || body["enabled"] != false {
		t.Errorf("history without git: %d %v", rec.Code, body)
	}
}

func TestSaveHistoryRollback(t *testing.T) {
	srv, _, dir := newGitServer(t)

	// Two saves produce two commits.
	v1 := "[Container]\nImage=docker.io/library/nginx:1.25\n"
	v2 := "[Container]\nImage=docker.io/library/nginx:1.26\n"
	for _, content := range []string{v1, v2} {
		rec, _ := doJSON(t, srv, "PUT", "/api/units/system/web.container",
			fmt.Sprintf(`{"content": %q}`, content))
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT: status %d: %s", rec.Code, rec.Body.String())
		}
	}

	rec, body := doJSON(t, srv, "GET", "/api/units/system/web.container/history", "")
	if rec.Code != http.StatusOK || body["enabled"] != true {
		t.Fatalf("history: %d %v", rec.Code, body)
	}
	commits := body["commits"].([]any)
	if len(commits) != 2 {
		t.Fatalf("got %d commits, want 2: %v", len(commits), commits)
	}
	newest := commits[0].(map[string]any)
	oldest := commits[1].(map[string]any)
	if newest["subject"] != "rookery: save web.container" {
		t.Errorf("newest subject = %v", newest["subject"])
	}
	if oldest["subject"] != "rookery: create web.container" {
		t.Errorf("oldest subject = %v", oldest["subject"])
	}

	// Content at the first commit is v1.
	oldHash := oldest["hash"].(string)
	rec, body = doJSON(t, srv, "GET", "/api/units/system/web.container/history/"+oldHash, "")
	if rec.Code != http.StatusOK || body["content"] != v1 {
		t.Fatalf("history show: %d %v", rec.Code, body["content"])
	}

	// Rollback restores v1 on disk and records a third commit.
	rec, body = doJSON(t, srv, "POST", "/api/units/system/web.container/rollback",
		fmt.Sprintf(`{"commit": %q}`, oldHash))
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback: status %d: %s", rec.Code, rec.Body.String())
	}
	if body["content"] != v1 {
		t.Errorf("rollback content = %v", body["content"])
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "web.container"))
	if err != nil || string(onDisk) != v1 {
		t.Fatalf("file after rollback = %q, %v", onDisk, err)
	}
	_, body = doJSON(t, srv, "GET", "/api/units/system/web.container/history", "")
	commits = body["commits"].([]any)
	if len(commits) != 3 || !strings.HasPrefix(commits[0].(map[string]any)["subject"].(string), "rookery: rollback web.container to ") {
		t.Errorf("history after rollback: %v", commits)
	}
}

func TestRollbackRecreatesDeletedUnit(t *testing.T) {
	srv, _, dir := newGitServer(t)
	content := "[Container]\nImage=docker.io/library/redis:7\n"
	doJSON(t, srv, "PUT", "/api/units/system/cache.container", fmt.Sprintf(`{"content": %q}`, content))

	_, body := doJSON(t, srv, "GET", "/api/units/system/cache.container/history", "")
	hash := body["commits"].([]any)[0].(map[string]any)["hash"].(string)

	rec, _ := doJSON(t, srv, "DELETE", "/api/units/system/cache.container", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(dir, "cache.container")); !os.IsNotExist(err) {
		t.Fatal("file still exists after delete")
	}

	rec, _ = doJSON(t, srv, "POST", "/api/units/system/cache.container/rollback",
		fmt.Sprintf(`{"commit": %q}`, hash))
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback after delete: status %d: %s", rec.Code, rec.Body.String())
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "cache.container"))
	if err != nil || string(onDisk) != content {
		t.Errorf("recreated file = %q, %v", onDisk, err)
	}
}

func TestRollbackValidatesBeforeWriting(t *testing.T) {
	srv, _, dir := newGitServer(t)
	content := "[Container]\nImage=x\n"
	doJSON(t, srv, "PUT", "/api/units/system/a.container", fmt.Sprintf(`{"content": %q}`, content))
	_, body := doJSON(t, srv, "GET", "/api/units/system/a.container/history", "")
	hash := body["commits"].([]any)[0].(map[string]any)["hash"].(string)

	// Swap in a rejecting validator: the rollback must be refused and the
	// file untouched.
	srv.validate = rejectValidator
	updated := "[Container]\nImage=y\n"
	if err := os.WriteFile(filepath.Join(dir, "a.container"), []byte(updated), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, _ := doJSON(t, srv, "POST", "/api/units/system/a.container/rollback",
		fmt.Sprintf(`{"commit": %q}`, hash))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid rollback: status %d, want 422", rec.Code)
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "a.container"))
	if string(onDisk) != updated {
		t.Errorf("file was modified by a rejected rollback: %q", onDisk)
	}
}

func TestHistoryShowRejectsBadCommit(t *testing.T) {
	srv, _, _ := newGitServer(t)
	doJSON(t, srv, "PUT", "/api/units/system/a.container", `{"content": "[Container]\n"}`)
	rec, _ := doJSON(t, srv, "GET", "/api/units/system/a.container/history/HEAD", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("symbolic ref: status %d, want 404", rec.Code)
	}
}
