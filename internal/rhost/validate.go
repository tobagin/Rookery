package rhost

import (
	"context"
	"fmt"
	"strings"

	"github.com/tobagin/rookery/internal/quadlet"
)

const noGenMarker = "__ROOKERY_NO_GENERATOR__"

// GeneratorCandidates lists the remote paths probed for the Quadlet
// generator. Package variable so the test shim — whose "remote host" is the
// local machine, which may itself have podman installed — can point it at
// nothing.
var GeneratorCandidates = []string{
	"/usr/lib/systemd/system-generators/podman-system-generator",
	"/usr/lib/systemd/user-generators/podman-user-generator",
	"/usr/libexec/podman/quadlet",
	"/usr/lib/podman/quadlet",
}

// ValidateRemote dry-runs content through the REMOTE host's own Quadlet
// generator — the same "validate with the Podman that will actually run
// this" rule as locally, which matters when hosts run different Podman
// versions. name must already have passed quadlet.CheckName.
func ValidateRemote(ctx context.Context, target string, userScope bool, name, content string) (quadlet.ValidationResult, error) {
	userFlag := ""
	if userScope {
		userFlag = " -user"
	}
	candidates := make([]string, len(GeneratorCandidates))
	for i, c := range GeneratorCandidates {
		candidates[i] = Quote(c)
	}
	script := fmt.Sprintf(`d=$(mktemp -d) || exit 125
trap 'rm -rf "$d"' EXIT
cat > "$d"/%s || exit 125
g=''
for c in %s; do
  if [ -x "$c" ]; then g="$c"; break; fi
done
if [ -z "$g" ]; then echo %s; exit 0; fi
QUADLET_UNIT_DIRS="$d" "$g" -dryrun%s 2>&1`,
		Quote(name), strings.Join(candidates, " "), noGenMarker, userFlag)

	out, err := Run(ctx, target, script, []byte(content))
	output := strings.TrimSpace(out)
	if err != nil {
		// 255 is ssh transport failure, 125 our scaffolding (mktemp/cat)
		// failing — neither is a verdict on the content. Anything else is
		// the generator rejecting the unit.
		if rerr, ok := err.(*Error); ok && !rerr.Transport() && rerr.ExitCode != 125 {
			return quadlet.ValidationResult{Available: true, Valid: false, Output: output, Generator: "remote:" + target}, nil
		}
		return quadlet.ValidationResult{}, err
	}
	if strings.Contains(output, noGenMarker) {
		return quadlet.ValidationResult{
			Available: false,
			Valid:     true,
			Output:    "podman quadlet generator not found on " + target + "; skipping validation",
		}, nil
	}
	return quadlet.ValidationResult{Available: true, Valid: true, Output: output, Generator: "remote:" + target}, nil
}
