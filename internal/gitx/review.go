package gitx

import (
	"context"
	"strings"
)

// DiffRange returns the unified diff (10 lines of context) from one commit to
// another. Renames are shown as delete plus add; external diff drivers and
// textconv are disabled.
func (r *Repo) DiffRange(ctx context.Context, from, to string) (string, error) {
	f, err := r.Resolve(ctx, from)
	if err != nil {
		return "", err
	}
	t, err := r.Resolve(ctx, to)
	if err != nil {
		return "", err
	}
	out, err := r.run(ctx, "diff", "--unified=10", "--no-renames", "--no-ext-diff", "--no-textconv", "--no-color", f, t, "--")
	return string(out), err
}

// Messages returns the commit messages in from..to, oldest first.
func (r *Repo) Messages(ctx context.Context, from, to string) ([]string, error) {
	f, err := r.Resolve(ctx, from)
	if err != nil {
		return nil, err
	}
	t, err := r.Resolve(ctx, to)
	if err != nil {
		return nil, err
	}
	out, err := r.run(ctx, "log", "--reverse", "--no-color", "--format=%B%x00", f+".."+t, "--")
	if err != nil {
		return nil, err
	}
	var msgs []string
	for m := range strings.SplitSeq(string(out), "\x00") {
		if m = strings.TrimSpace(m); m != "" {
			msgs = append(msgs, m)
		}
	}
	return msgs, nil
}
