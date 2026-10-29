package gitx

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsAncestorExact(t *testing.T) {
	ctx := context.Background()
	r, dir := fixture(ctx, t)
	base := head(ctx, t, r)
	writeFile(t, filepath.Join(dir, "source.txt"), "second\n")
	git(ctx, t, dir, "commit", "-am", "second")
	next := head(ctx, t, r)
	for _, tc := range []struct {
		old, next string
		want      bool
	}{{base, next, true}, {next, base, false}, {next, next, true}} {
		got, err := r.IsAncestor(ctx, tc.old, tc.next)
		if err != nil || got != tc.want {
			t.Fatalf("ancestry = %v, %v; want %v", got, err, tc.want)
		}
	}
	if _, err := r.IsAncestor(ctx, "main", next); err == nil {
		t.Fatal("mutable ref accepted for exact ancestry")
	}
	if _, err := r.IsAncestor(ctx, strings.Repeat("f", 40), next); err == nil {
		t.Fatal("missing commit became ancestry evidence")
	}
}

func TestRemotePushURLRequiresSingleTarget(t *testing.T) {
	r, _ := fixture(t.Context(), t)
	if _, err := r.RemotePushURL(t.Context(), "-bad"); err == nil {
		t.Fatal("accepted option")
	}
	git(t.Context(), t, r.Root, "remote", "add", "origin", "https://github.com/example/project.git")
	got, err := r.RemotePushURL(t.Context(), "origin")
	if err != nil || got != "https://github.com/example/project.git" {
		t.Fatalf("%q %v", got, err)
	}
	git(t.Context(), t, r.Root, "remote", "set-url", "--add", "--push", "origin", "https://github.com/example/second.git")
	git(t.Context(), t, r.Root, "remote", "set-url", "--add", "--push", "origin", "https://github.com/example/third.git")
	if _, err := r.RemotePushURL(t.Context(), "origin"); err == nil {
		t.Fatal("multiple push destinations accepted")
	}
}

func TestRemotePushURLRejectsChainedRewrites(t *testing.T) {
	for _, kind := range []string{"insteadOf", "pushInsteadOf"} {
		t.Run(kind, func(t *testing.T) {
			r, _ := fixture(t.Context(), t)
			git(t.Context(), t, r.Root, "remote", "add", "origin", "short:project.git")
			git(t.Context(), t, r.Root, "config", "url.https://github.com/first/."+kind, "short:")
			got, err := r.RemotePushURL(t.Context(), "origin")
			if err != nil || got != "https://github.com/first/project.git" {
				t.Fatalf("single rewrite: %q %v", got, err)
			}
			git(t.Context(), t, r.Root, "config", "url.https://github.com/second/."+kind, "https://github.com/first/")
			if _, err := r.RemotePushURL(t.Context(), "origin"); err == nil {
				t.Fatal("resolved URL could be rewritten to a different target")
			}
		})
	}
}

func TestResolvedPushURLUsesPushDestination(t *testing.T) {
	r, _ := fixture(t.Context(), t)
	fetch := filepath.Join(t.TempDir(), "fetch.git")
	push := filepath.Join(t.TempDir(), "push.git")
	git(t.Context(), t, r.Root, "init", "--bare", fetch)
	git(t.Context(), t, r.Root, "init", "--bare", push)
	git(t.Context(), t, r.Root, "remote", "add", "origin", fetch)
	git(t.Context(), t, r.Root, "remote", "set-url", "--push", "origin", push)
	url, err := r.RemotePushURL(t.Context(), "origin")
	if err != nil || url != push {
		t.Fatalf("url %q %v", url, err)
	}
	got, err := r.RemoteBranchSHAAtURL(t.Context(), url, "feature")
	if err != nil || got != "" {
		t.Fatalf("fresh push branch: %q %v", got, err)
	}
	sha := head(t.Context(), t, r)
	if err = r.PushLeaseToURL(t.Context(), url, "feature", sha, ""); err != nil {
		t.Fatal(err)
	}
	got, err = r.RemoteBranchSHAAtURL(t.Context(), url, "feature")
	if err != nil || got != sha {
		t.Fatalf("push branch: %q %v", got, err)
	}
	if _, err = r.RemoteBranchSHAAtURL(t.Context(), "-malicious", "feature"); err == nil {
		t.Fatal("URL option accepted")
	}
	if err = r.PushLeaseToURL(t.Context(), url, "feature", sha, "invalid"); err == nil {
		t.Fatal("invalid expected OID accepted")
	}
}
