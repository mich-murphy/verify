package cli

import (
	"cmp"
	"errors"
	"os"
	"strings"

	"github.com/mich-murphy/verify/internal/review"
	"github.com/mich-murphy/verify/internal/ship"
	"github.com/mich-murphy/verify/internal/state"
)

const reasonUnavailable = "reviewer CLI unavailable"

// describe composes the PR title and description for HEAD. A reviewer CLI result is cached
// on the branch record by SHA, so a rerun makes no call. The commit-derived fallback is not
// cached, so a rerun tries the CLI again. A dry run never calls the CLI. This is not a review:
// it touches neither the review record nor the review attempt count.
func (v *view) describe(env *Env, dry bool) (state.Description, *viewErr) {
	if d := v.recs.br.Description; d != nil && d.SHA == v.sha {
		return *d, nil
	}
	msgs, _ := v.repo.Messages(env.Ctx, v.loaded.MergeBase, v.sha)
	subjects := make([]string, len(msgs))
	for i, m := range msgs {
		subjects[i], _, _ = strings.Cut(m, "\n")
	}
	fallback := func(reason string) state.Description {
		title, body := ship.FallbackDescription(v.branch, subjects, reason)
		return state.Description{SHA: v.sha, Title: title, Body: body, Source: state.DescribedByCommits, Reason: reason}
	}
	if dry {
		return fallback("dry run, reviewer not called"), nil
	}
	if len(msgs) == 0 {
		return fallback(reasonUnavailable), nil
	}
	rv, other, err := selectReviewers(cmp.Or(os.Getenv("VERIFY_REVIEWER"), "auto"))
	if err != nil {
		return fallback(reasonUnavailable), &viewErr{ExitUsage, err.Error(), "set VERIFY_REVIEWER to auto, claude or codex"}
	}
	diff, err := v.repo.DiffRange(env.Ctx, v.loaded.MergeBase, v.sha)
	if err != nil {
		return fallback("diff unavailable"), nil
	}
	packet, err := review.Build(review.Input{Diff: diff, Messages: msgs})
	if err != nil {
		return fallback("diff too large to describe"), nil
	}
	rv = review.WithTask(rv, review.DescribeTask)
	raw, _, err := rv.Run(env.Ctx, packet)
	if errors.Is(err, review.ErrUnavailable) && other != nil {
		rv = review.WithTask(other, review.DescribeTask)
		raw, _, err = rv.Run(env.Ctx, packet)
	}
	if err != nil {
		return fallback(reasonUnavailable), nil
	}
	title, body, err := review.ParseDescription(raw)
	if err != nil {
		return fallback("reviewer output invalid"), nil
	}
	d := state.Description{SHA: v.sha, Title: title, Body: body, Source: rv.Name()}
	if err := v.store.UpdateBranch(env.Ctx, v.branch, func(b *state.Branch) error { b.Description = &d; return nil }); err != nil {
		return d, &viewErr{ExitBlocked, err.Error(), "resolve the state error"}
	}
	return d, nil
}
