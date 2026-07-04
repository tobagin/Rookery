package quadlet

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ValidationResult reports what the host's own Quadlet generator said about
// a unit file. Validating with the host generator (not a vendored parser)
// means Rookery always agrees with the Podman version actually running.
type ValidationResult struct {
	Available bool   `json:"available"` // generator binary found on this host
	Valid     bool   `json:"valid"`
	Output    string `json:"output"`
	Generator string `json:"generator,omitempty"`
}

var generatorCandidates = []string{
	"/usr/lib/systemd/system-generators/podman-system-generator",
	"/usr/lib/systemd/user-generators/podman-user-generator",
	"/usr/libexec/podman/quadlet",
	"/usr/lib/podman/quadlet",
}

// FindGenerator locates the Quadlet generator binary on this host, or
// returns "" if Podman's generator is not installed.
func FindGenerator() string {
	for _, p := range generatorCandidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// Validate writes content to a temp dir and runs the host generator over it
// in -dryrun mode. If no generator is installed, the result is marked
// unavailable (and Valid=true so callers may proceed with a warning).
func Validate(ctx context.Context, userScope bool, fileName, content string) (ValidationResult, error) {
	gen := FindGenerator()
	if gen == "" {
		return ValidationResult{Available: false, Valid: true, Output: "podman quadlet generator not found on this host; skipping validation"}, nil
	}
	dir, err := os.MkdirTemp("", "rookery-validate-*")
	if err != nil {
		return ValidationResult{}, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(content), 0o644); err != nil {
		return ValidationResult{}, err
	}

	args := []string{"-dryrun"}
	if userScope {
		args = append(args, "-user")
	}
	cmd := exec.CommandContext(ctx, gen, args...)
	cmd.Env = append(os.Environ(), "QUADLET_UNIT_DIRS="+dir)
	out, err := cmd.CombinedOutput()
	res := ValidationResult{Available: true, Generator: gen, Output: strings.TrimSpace(string(out))}
	if err != nil {
		if _, isExit := err.(*exec.ExitError); !isExit {
			return res, err
		}
		res.Valid = false
		return res, nil
	}
	res.Valid = true
	return res, nil
}
