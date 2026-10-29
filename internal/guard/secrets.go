package guard

import (
	"context"
	"fmt"
	"sync"

	"github.com/betterleaks/betterleaks/config"
	"github.com/betterleaks/betterleaks/detect"
	"github.com/betterleaks/betterleaks/logging"
	"github.com/betterleaks/betterleaks/sources"
	"github.com/rs/zerolog"

	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/state"
)

// Config/allowlist decision: only betterleaks' built-in default rules are
// used. A repo's .betterleaks.toml/.gitleaks.toml and .betterleaksignore/
// .gitleaksignore are never loaded (the library does not read them unless the
// CLI wires them), and inline "gitleaks:allow" comments are ignored
// (IgnoreGitleaksAllow), so the candidate commit cannot hide its own secrets.

// quiet silences the library's global zerolog logger once.
var quiet sync.Once

func scanSecrets(ctx context.Context, in Input) ([]state.Finding, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("secret scan: %w", err)
	}
	quiet.Do(func() { logging.Logger = zerolog.Nop() })

	cfg, err := config.Default()
	if err != nil {
		return nil, fmt.Errorf("secret scan config: %w", err)
	}
	det := detect.NewDetectorContext(ctx, cfg, detect.ValidationOptions{}) // validation (network) stays off
	det.IgnoreGitleaksAllow = true
	det.MaxDecodeDepth = 5
	det.SkipFindingAppend = true

	var src sources.Source = rangeSource{repo: in.Repo, base: in.Base, sha: in.SHA, skip: det.SkipFunc()}
	if in.Fast {
		src = workingSource{diff: in.Diff, sha: in.SHA, skip: det.SkipFunc()}
	}

	var out []state.Finding
	seen := map[string]bool{}
	var scanErrs []error
	for res := range det.Run(ctx, src) {
		if res.Err != nil {
			scanErrs = append(scanErrs, res.Err)
			continue
		}
		f := res.Finding
		// Never read f.Secret/f.Match/f.Line: the value must not leak.
		file := f.Attributes[sources.AttrPath]
		commit := f.Attributes[sources.AttrGitSHA]
		nf := finding(Secrets, file, f.StartLine, state.SeverityBlock,
			fmt.Sprintf("possible secret (%s) in commit %s; remove it and rotate the credential", f.RuleID, shortSHA(commit)))
		if !seen[nf.ID] {
			seen[nf.ID] = true
			out = append(out, nf)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("secret scan: %w", err)
	}
	if len(scanErrs) > 0 {
		// Errors come from git/diff parsing, not match content.
		return nil, fmt.Errorf("secret scan failed (%d error(s)): %w", len(scanErrs), scanErrs[0])
	}
	return out, nil
}

// rangeSource feeds betterleaks one fragment per hunk of added lines in each
// commit of base..sha, using Gate's hardened git runner (clean env). Every
// commit is scanned individually, so a secret added then removed within the
// range is still found. Binary files are skipped (archive scanning, which the
// library git source offered, is dropped). skip is the built-in rules' global
// prefilter (for example go.sum and lockfiles), applied per file as the
// library git source does.
type rangeSource struct {
	repo      *gitx.Repo
	base, sha string
	skip      sources.SkipFunc
}

func (s rangeSource) Fragments(ctx context.Context, yield sources.FragmentsFunc) error {
	return s.repo.AddedHunks(ctx, s.base, s.sha, func(h gitx.AddedHunk) error {
		attrs := map[string]string{
			sources.AttrPath:     h.Path,
			sources.AttrGitSHA:   h.Commit,
			sources.AttrResource: sources.ResourceGitPatchContent,
		}
		if s.skip != nil && s.skip(attrs) {
			return nil
		}
		return yield(sources.Fragment{Raw: h.Text, StartLine: h.StartLine, Attributes: attrs}, nil)
	})
}

func shortSHA(s string) string { return s[:min(12, len(s))] }

// workingSource scans added working-tree lines including untracked files.
type workingSource struct {
	diff *gitx.LineDiff
	sha  string
	skip sources.SkipFunc
}

func (s workingSource) Fragments(_ context.Context, yield sources.FragmentsFunc) error {
	return gitx.YieldWorkingHunks(s.sha, s.diff.Added, func(h gitx.AddedHunk) error {
		attrs := map[string]string{sources.AttrPath: h.Path, sources.AttrGitSHA: h.Commit, sources.AttrResource: sources.ResourceGitPatchContent}
		if s.skip != nil && s.skip(attrs) {
			return nil
		}
		return yield(sources.Fragment{Raw: h.Text, StartLine: h.StartLine, Attributes: attrs}, nil)
	})
}
