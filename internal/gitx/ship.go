package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// validRemoteArg rejects values that could be parsed as options or break a refspec.
func validRemoteArg(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && !strings.ContainsAny(s, " \t\r\n\x00:")
}

// netGit runs git for network operations. Unlike run it keeps the user's
// environment and config, because remotes need the user's credentials.
func (r *Repo) netGit(ctx context.Context, args ...string) ([]byte, error) {
	// #nosec G204 -- fixed git executable; callers validate remote and ref arguments.
	cmd := exec.CommandContext(ctx, r.git, append([]string{"-C", r.Root}, args...)...)
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch k {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR":
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "GIT_TERMINAL_PROMPT=0")
	cmd.Env = env
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out.Bytes(), nil
}

// RemoteBranchSHA returns the commit at refs/heads/<branch> on remote, or ""
// when the branch does not exist there.
func (r *Repo) RemoteBranchSHA(ctx context.Context, remote, branch string) (string, error) {
	if !validRemoteArg(remote) || !validRemoteArg(branch) {
		return "", errors.New("invalid remote or branch name")
	}
	ref := "refs/heads/" + branch
	out, err := r.netGit(ctx, "ls-remote", remote, ref)
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == ref {
			return f[0], nil
		}
	}
	return "", nil
}

// PushLease pushes sha to refs/heads/<branch> on remote, only if the remote
// branch is still at expected ("" means it must not exist).
func (r *Repo) PushLease(ctx context.Context, remote, branch, sha, expected string) error {
	if !validRemoteArg(remote) || !validRemoteArg(branch) {
		return errors.New("invalid remote or branch name")
	}
	if !oidPattern.MatchString(sha) {
		return errors.New("push needs an exact commit OID")
	}
	ref := "refs/heads/" + branch
	_, err := r.netGit(ctx, "push", "--force-with-lease="+ref+":"+expected, remote, sha+":"+ref)
	return err
}

// FirstSubject returns the subject of the oldest commit in base..sha, or "".
func (r *Repo) FirstSubject(ctx context.Context, base, sha string) (string, error) {
	b, err := r.Resolve(ctx, base)
	if err != nil {
		return "", err
	}
	s, err := r.Resolve(ctx, sha)
	if err != nil {
		return "", err
	}
	out, err := r.run(ctx, "log", "--reverse", "--format=%s", b+".."+s)
	if err != nil {
		return "", err
	}
	first, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(first), nil
}

// RemotePushURL resolves exactly one push destination using normal Git config.
// Chained rewrites are rejected: passing an already-resolved URL to ls-remote or
// push must not silently rewrite it to a different repository a second time.
func (r *Repo) RemotePushURL(ctx context.Context, remote string) (string, error) {
	if !validRemoteArg(remote) {
		return "", errors.New("invalid remote name")
	}
	out, err := r.netGit(ctx, "remote", "get-url", "--push", "--all", remote)
	if err != nil {
		return "", err
	}
	urls := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(urls) != 1 || !validPushURL(urls[0]) {
		return "", fmt.Errorf("remote %q has no unique push URL", remote)
	}
	if err := r.rejectFurtherRewrite(ctx, urls[0]); err != nil {
		return "", err
	}
	return urls[0], nil
}

func (r *Repo) rejectFurtherRewrite(ctx context.Context, resolved string) error {
	out, err := r.netGit(ctx, "config", "--null", "--get-regexp", `^url\..*\.(insteadof|pushinsteadof)$`)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil // No rewrite rules are configured.
	}
	if err != nil {
		return errors.New("cannot inspect Git URL rewrite configuration")
	}
	for entry := range bytes.SplitSeq(out, []byte{0}) {
		_, prefix, ok := bytes.Cut(entry, []byte{'\n'})
		if ok && strings.HasPrefix(resolved, string(prefix)) {
			return errors.New("resolved push URL matches another Git URL rewrite; configure an unambiguous direct push destination")
		}
	}
	return nil
}

// validPushURL admits URL syntax (including ssh colons) without allowing
// Git option injection or whitespace/control characters in an argument.
func validPushURL(url string) bool {
	return url != "" && !strings.HasPrefix(url, "-") && !strings.ContainsAny(url, " \t\r\n\x00")
}

// RemoteBranchSHAAtURL reads the exact push destination, not a distinct fetch
// URL associated with the same remote name.
func (r *Repo) RemoteBranchSHAAtURL(ctx context.Context, url, branch string) (string, error) {
	if !validPushURL(url) || !validRemoteArg(branch) {
		return "", errors.New("invalid push URL or branch")
	}
	ref := "refs/heads/" + branch
	out, err := r.netGit(ctx, "ls-remote", url, ref)
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 && f[1] == ref {
			return f[0], nil
		}
	}
	return "", nil
}

// PushLeaseToURL pushes only to the URL previously resolved and inspected.
func (r *Repo) PushLeaseToURL(ctx context.Context, url, branch, sha, expected string) error {
	if !validPushURL(url) || !validRemoteArg(branch) {
		return errors.New("invalid push URL or branch")
	}
	if !oidPattern.MatchString(sha) || (expected != "" && !oidPattern.MatchString(expected)) {
		return errors.New("push needs exact commit OIDs")
	}
	ref := "refs/heads/" + branch
	_, err := r.netGit(ctx, "push", "--force-with-lease="+ref+":"+expected, url, sha+":"+ref)
	return err
}
