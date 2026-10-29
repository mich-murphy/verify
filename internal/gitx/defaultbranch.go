package gitx

import (
	"context"
	"strings"
)

// DefaultBranch is the branch origin/HEAD points at, or "main" when the
// repository has no origin/HEAD.
func (r *Repo) DefaultBranch(ctx context.Context) string {
	out, err := r.run(ctx, "symbolic-ref", "--short", "-q", "refs/remotes/origin/HEAD")
	if b := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/"); err == nil && b != "" && !strings.HasPrefix(b, "-") {
		return b
	}
	return "main"
}
