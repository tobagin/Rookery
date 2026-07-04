// Package gitstore keeps a Quadlet directory under git, giving every save
// an audit trail and a rollback point. It shells out to the git binary and
// stores nothing outside the directory itself — the repo IS the history,
// readable with plain git if Rookery disappears tomorrow.
package gitstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/tobagin/rookery/internal/rhost"
)

// ErrNotRepo reports a directory that isn't under git (and creation wasn't
// requested).
var ErrNotRepo = errors.New("directory is not a git repository")

// Commit is one history entry for a unit file.
type Commit struct {
	Hash    string `json:"hash"`
	Time    int64  `json:"time"` // unix seconds
	Subject string `json:"subject"`
}

var hashRe = regexp.MustCompile(`^[0-9a-f]{4,40}$`)

// Store operates on one directory's repository. With ssh set, every git
// invocation runs on that remote host instead of locally.
type Store struct {
	dir string
	ssh string
}

// Open returns a Store for dir. With create, a repository is initialized
// when none exists; without it, a plain directory yields ErrNotRepo so
// callers can silently skip git features.
func Open(dir string, create bool) (*Store, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("git binary not found: %w", err)
	}
	s := &Store{dir: dir}
	if _, err := os.Stat(dir); err != nil {
		if !create {
			return nil, ErrNotRepo
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	if _, err := s.run(context.Background(), "rev-parse", "--git-dir"); err != nil {
		if !create {
			return nil, ErrNotRepo
		}
		if _, err := s.run(context.Background(), "init", "--quiet"); err != nil {
			return nil, fmt.Errorf("git init %s: %w", dir, err)
		}
	}
	return s, nil
}

// OpenRemote returns a Store for a directory on an ssh target. It never
// initializes a repository on the remote host — history is enabled only
// when the directory is already tracked there; anything else (no git, no
// repo, no directory) yields ErrNotRepo so callers silently skip.
func OpenRemote(ctx context.Context, target, dir string) (*Store, error) {
	s := &Store{dir: dir, ssh: target}
	if _, err := s.run(ctx, "rev-parse", "--git-dir"); err != nil {
		var rerr *rhost.Error
		if errors.As(err, &rerr) && rerr.Transport() {
			return nil, err // unreachable host is worth reporting, not skipping
		}
		return nil, ErrNotRepo
	}
	return s, nil
}

// Dir returns the directory this store tracks.
func (s *Store) Dir() string { return s.dir }

// run executes git in the store directory with a fixed committer identity,
// so commits work on hosts where git was never configured.
func (s *Store) run(ctx context.Context, args ...string) (string, error) {
	full := append([]string{
		"git",
		"-C", s.dir,
		"-c", "user.name=rookery",
		"-c", "user.email=rookery@localhost",
	}, args...)
	if s.ssh != "" {
		out, err := rhost.Run(ctx, s.ssh, rhost.QuoteArgv(full), nil)
		if err != nil {
			// %w keeps the *rhost.Error chain so callers can tell transport
			// failures from git verdicts.
			return out, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return out, nil
	}
	cmd := exec.CommandContext(ctx, full[0], full[1:]...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// CommitFile stages name (including its deletion) and commits it with
// message. A no-op change commits nothing and returns nil.
func (s *Store) CommitFile(ctx context.Context, name, message string) error {
	if _, err := s.run(ctx, "add", "-A", "--", name); err != nil {
		return err
	}
	if _, err := s.run(ctx, "diff", "--cached", "--quiet", "--", name); err == nil {
		return nil // nothing staged
	}
	_, err := s.run(ctx, "commit", "--quiet", "-m", message, "--", name)
	return err
}

// History lists the commits that touched name, newest first.
func (s *Store) History(ctx context.Context, name string, limit int) ([]Commit, error) {
	out, err := s.run(ctx, "log", "-n", strconv.Itoa(limit),
		"--format=%H%x1f%ct%x1f%s", "--", name)
	if err != nil {
		// A file with no history yet (or an empty repo) is not an error.
		if strings.Contains(err.Error(), "does not have any commits") {
			return []Commit{}, nil
		}
		return nil, err
	}
	commits := []Commit{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		parts := strings.Split(line, "\x1f")
		if len(parts) != 3 {
			continue
		}
		t, _ := strconv.ParseInt(parts[1], 10, 64)
		commits = append(commits, Commit{Hash: parts[0], Time: t, Subject: parts[2]})
	}
	return commits, nil
}

// Show returns name's content at the given commit.
func (s *Store) Show(ctx context.Context, commit, name string) (string, error) {
	if !hashRe.MatchString(commit) {
		return "", fmt.Errorf("invalid commit hash %q", commit)
	}
	return s.run(ctx, "show", commit+":"+name)
}
