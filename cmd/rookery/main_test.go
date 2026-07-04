package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverQuadletUsers(t *testing.T) {
	root := t.TempDir()
	withTree := filepath.Join(root, "alice")
	withoutTree := filepath.Join(root, "bob")
	if err := os.MkdirAll(filepath.Join(withTree, ".config", "containers", "systemd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(withoutTree, 0o755); err != nil {
		t.Fatal(err)
	}
	passwd := filepath.Join(root, "passwd")
	content := "root:x:0:0:root:/root:/bin/bash\n" +
		"daemon:x:2:2::/nonexistent:/usr/sbin/nologin\n" +
		"alice:x:1000:1000::" + withTree + ":/bin/bash\n" +
		"bob:x:1001:1001::" + withoutTree + ":/bin/bash\n" +
		"nobody:x:65534:65534::" + withTree + ":/usr/sbin/nologin\n" +
		"broken line without colons\n"
	if err := os.WriteFile(passwd, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := discoverQuadletUsers(passwd)
	if len(got) != 1 || got[0] != "alice" {
		t.Errorf("discovered = %v, want [alice]", got)
	}
	if got := discoverQuadletUsers(filepath.Join(root, "missing")); got != nil {
		t.Errorf("missing passwd = %v, want nil", got)
	}
}
