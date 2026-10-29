package gitx

import (
	"context"
	"errors"
	"os/exec"
)

// IsAncestor compares exact immutable commits. Exit 1 is a normal negative
// ancestry answer, not an error that can be mistaken for affirmative evidence.
func (r *Repo) IsAncestor(ctx context.Context, ancestor, candidate string) (bool, error) {
	if !oidPattern.MatchString(ancestor) || !oidPattern.MatchString(candidate) {
		return false, errors.New("ancestry comparison requires exact OIDs")
	}
	_, err := r.run(ctx, "merge-base", "--is-ancestor", ancestor, candidate)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}
