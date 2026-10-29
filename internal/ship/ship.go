// Package ship pushes the checked commit, opens or updates the PR and sets the
// verify/verified status (SPEC §7). Eligibility is decided by the caller.
package ship

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/gitx"
)

const cmdTimeout = 2 * time.Minute

// Step outcomes.
const (
	Done    = "done"
	Failed  = "failed"
	Planned = "planned"
	Pending = "pending"
)

// Options describes one ship.
type Options struct {
	Repo    *gitx.Repo
	Remote  string
	Branch  string
	SHA     string
	Base    string
	Title   string
	Body    string
	Lane    string
	DryRun  bool
	Target  string // resolved GitHub owner/repo; never inferred by gh from cwd
	pushURL string // the same destination used for lease read and push
}

// Step is one sub-step outcome.
type Step struct {
	Name   string
	Status string
	Detail string
}

// Report lists every sub-step in order.
type Report struct {
	Steps []Step
	PRURL string
}

type action struct {
	name string
	plan string
	run  func(ctx context.Context, o Options, rep *Report) (string, error)
}

func actions(o Options) []action {
	ref := "refs/heads/" + o.Branch
	return []action{
		{"push", fmt.Sprintf("git push --force-with-lease=%s:<remote value> %s %s:%s", ref, o.Remote, o.SHA, ref), pushStep},
		{"pr", fmt.Sprintf("gh pr view %s --repo github.com/%s, then gh pr create or gh pr edit (base %s)", o.Branch, o.Target, o.Base), prStep},
		{"status", fmt.Sprintf("gh api --hostname github.com -X POST repos/%s/statuses/%s -f state=success -f context=verify/verified", o.Target, o.SHA), statusStep},
	}
}

// Run performs (or, with DryRun, plans) the ship. On error the report shows
// what completed and what remains; rerunning is safe because each step is idempotent.
func Run(ctx context.Context, o Options) (Report, error) {
	var rep Report
	target, err := remoteTarget(ctx, o)
	if err != nil {
		rep.Steps = []Step{{"push", Failed, err.Error()}, {"pr", Pending, ""}, {"status", Pending, ""}}
		return rep, err
	}
	o.Target, o.pushURL = target.repo, target.pushURL
	acts := actions(o)
	if o.DryRun {
		for _, a := range acts {
			rep.Steps = append(rep.Steps, Step{a.name, Planned, a.plan})
		}
		return rep, nil
	}
	for i, a := range acts {
		detail, err := a.run(ctx, o, &rep)
		if err != nil {
			rep.Steps = append(rep.Steps, Step{a.name, Failed, err.Error()})
			for _, rest := range acts[i+1:] {
				rep.Steps = append(rep.Steps, Step{rest.name, Pending, ""})
			}
			return rep, fmt.Errorf("%s: %w", a.name, err)
		}
		rep.Steps = append(rep.Steps, Step{a.name, Done, detail})
	}
	return rep, nil
}

func pushStep(ctx context.Context, o Options, _ *Report) (string, error) {
	expected, err := o.Repo.RemoteBranchSHAAtURL(ctx, o.pushURL, o.Branch)
	if err != nil {
		return "", fmt.Errorf("cannot read selected push target branch for lease")
	}
	if err := o.Repo.PushLeaseToURL(ctx, o.pushURL, o.Branch, o.SHA, expected); err != nil {
		return "", fmt.Errorf("selected push target rejected lease push")
	}
	if expected == "" {
		expected = "absent"
	}
	return fmt.Sprintf("pushed %s to %s/%s (lease %s)", short(o.SHA), o.Remote, o.Branch, short(expected)), nil
}

type prInfo struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

func prStep(ctx context.Context, o Options, rep *Report) (string, error) {
	dir, err := os.MkdirTemp("", "verify-pr-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	bodyFile := filepath.Join(dir, "body.md")
	if err := os.WriteFile(bodyFile, []byte(o.Body), 0o600); err != nil {
		return "", err
	}
	// A failing view means no PR exists for the branch (or gh is broken, which create then reports).
	var pr prInfo
	if out, verr := gh(ctx, o, "pr", "view", o.Branch, "--repo", "github.com/"+o.Target, "--json", "number,url,state"); verr == nil {
		if jerr := json.Unmarshal(out, &pr); jerr != nil {
			return "", fmt.Errorf("unreadable gh pr view output: %w", jerr)
		}
	}
	if pr.State == "OPEN" && pr.Number > 0 {
		if _, err := gh(ctx, o, "pr", "edit", fmt.Sprint(pr.Number), "--repo", "github.com/"+o.Target, "--body-file", bodyFile); err != nil {
			return "", err
		}
		rep.PRURL = pr.URL
		return "updated PR " + pr.URL, nil
	}
	out, err := gh(ctx, o, "pr", "create", "--repo", "github.com/"+o.Target, "--head", o.Branch, "--base", o.Base, "--title", o.Title, "--body-file", bodyFile)
	if err != nil {
		return "", err
	}
	rep.PRURL = strings.TrimSpace(string(out))
	return "created PR " + rep.PRURL, nil
}

func statusStep(ctx context.Context, o Options, _ *Report) (string, error) {
	desc := "Verify checked this exact commit (" + o.Lane + " lane)"
	_, err := gh(ctx, o, "api", "--hostname", "github.com", "-X", "POST", "repos/"+o.Target+"/statuses/"+o.SHA,
		"-f", "state=success", "-f", "context=verify/verified", "-f", "description="+desc)
	if err != nil {
		return "", err
	}
	return "set verify/verified=success on " + short(o.SHA), nil
}

func gh(ctx context.Context, o Options, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...) // #nosec G204 -- fixed executable; arguments are built by Verify.
	cmd.Dir = o.Repo.Root
	cmd.Env = append(os.Environ(), "GH_HOST=github.com", "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, errors.New("gh " + args[0] + ": " + msg)
	}
	return out.Bytes(), nil
}

var repoPart = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

type pushTarget struct{ repo, pushURL string }

// remoteTarget resolves the actual selected push URL before any push or gh operation.
func remoteTarget(ctx context.Context, o Options) (pushTarget, error) {
	if o.Repo == nil || o.Remote == "" {
		return pushTarget{}, fmt.Errorf("invalid selected remote")
	}
	raw, err := o.Repo.RemotePushURL(ctx, o.Remote)
	if err != nil || raw == "" {
		return pushTarget{}, fmt.Errorf("selected remote has no unique GitHub push URL")
	}
	path, err := githubPath(raw)
	if err != nil {
		return pushTarget{}, err
	}
	path = strings.TrimSuffix(path, ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || !repoPart.MatchString(parts[0]) || !repoPart.MatchString(parts[1]) || parts[0] == "." || parts[1] == "." || parts[0] == ".." || parts[1] == ".." {
		return pushTarget{}, fmt.Errorf("selected remote has invalid GitHub owner/repo")
	}
	return pushTarget{repo: path, pushURL: raw}, nil
}

func githubPath(raw string) (string, error) {
	if strings.HasPrefix(raw, "git@github.com:") {
		return strings.TrimPrefix(raw, "git@github.com:"), nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("selected remote has invalid GitHub URL")
	}
	validTransport := u.Scheme == "https" || u.Scheme == "ssh" && u.User.Username() == "git"
	if u.Hostname() != "github.com" || u.Port() != "" || !validTransport || u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("selected remote is not a GitHub repository")
	}
	return strings.TrimPrefix(u.Path, "/"), nil
}
func short(s string) string { return s[:min(12, len(s))] }
