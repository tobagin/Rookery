// Package rhosttest provides the ssh shim shared by tests that exercise
// remote-host paths without a real network.
package rhosttest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tobagin/rookery/internal/rhost"
)

// InstallShim points rhost.SSHCommand at a script that mimics ssh's
// contract: skip options, take a target, join the remaining args, and run
// them through a shell — which is exactly what sshd does on the remote
// end. The "remote host" is therefore this machine's filesystem, making
// callers integration tests of the full quoting/transport path.
func InstallShim(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	shim := filepath.Join(dir, "fakessh")
	script := `#!/bin/sh
# skip ssh options (-o value pairs and lone flags)
while [ $# -gt 0 ]; do
  case "$1" in
    -o) shift 2 ;;
    -*) shift ;;
    *) break ;;
  esac
done
target=$1; shift
[ "$1" = "--" ] && shift
exec sh -c "$*"
`
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	old := rhost.SSHCommand
	rhost.SSHCommand = []string{shim, "-o", "BatchMode=yes"}
	t.Cleanup(func() { rhost.SSHCommand = old })

	// The "remote host" is this machine, which may have podman installed
	// (Fedora dev boxes, current GitHub runners). Pin the generator probe to
	// nothing so validation deterministically degrades to unavailable.
	oldGen := rhost.GeneratorCandidates
	rhost.GeneratorCandidates = []string{"/nonexistent/rookery-test-quadlet"}
	t.Cleanup(func() { rhost.GeneratorCandidates = oldGen })
}
