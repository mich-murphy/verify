package gitx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffLinesAndParse(t *testing.T) {
	ctx := t.Context()
	repo, dir := fixture(ctx, t)
	base := head(ctx, t, repo)
	writeFile(t, filepath.Join(dir, "source.txt"), "base\nadded\n")
	writeFile(t, filepath.Join(dir, "new.txt"), "n1\nn2\n")
	writeFile(t, filepath.Join(dir, "gone.txt"), "g1\n")
	writeFile(t, filepath.Join(dir, "mode.sh"), "echo\n")
	writeFile(t, filepath.Join(dir, "bin.dat"), "a\x00b")
	git(ctx, t, dir, "add", "-A")
	git(ctx, t, dir, "commit", "-m", "setup")
	mid := head(ctx, t, repo)
	git(ctx, t, dir, "rm", "-q", "gone.txt")
	writeFile(t, filepath.Join(dir, "source.txt"), "changed\nadded\n")
	writeFile(t, filepath.Join(dir, "bin.dat"), "a\x00c")
	if err := os.Chmod(filepath.Join(dir, "mode.sh"), 0o700); err != nil { // #nosec G302 -- exec bit is the test subject
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "added.txt"), "x\n")
	git(ctx, t, dir, "add", "-A")
	git(ctx, t, dir, "commit", "-m", "second")

	d, err := repo.DiffLines(ctx, base, mid)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.NewFiles) != 4 || len(d.Modified) != 1 || d.Modified[0] != "source.txt" {
		t.Fatalf("first diff files: %+v", d)
	}
	if len(d.Added) != 5 {
		t.Fatalf("added: %+v", d.Added)
	}
	d, err = repo.DiffLines(ctx, mid, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if len(d.DeletedFiles) != 1 || d.DeletedFiles[0] != "gone.txt" {
		t.Fatalf("deleted: %+v", d.DeletedFiles)
	}
	if len(d.NewFiles) != 1 || d.NewFiles[0] != "added.txt" {
		t.Fatalf("new: %+v", d.NewFiles)
	}
	var mod []string
	mod = append(mod, d.Modified...)
	if strings.Join(mod, ",") != "bin.dat,mode.sh,source.txt" {
		t.Fatalf("modified (binary/mode-only): %v", mod)
	}
	wantAdd := Line{Path: "source.txt", Number: 1, Text: "changed"}
	wantRem := Line{Path: "source.txt", Number: 1, Text: "base"}
	if !containsLine(d.Added, wantAdd) || !containsLine(d.Removed, wantRem) {
		t.Fatalf("lines: +%v -%v", d.Added, d.Removed)
	}
	if !containsLine(d.Removed, Line{Path: "gone.txt", Number: 1, Text: "g1"}) {
		t.Fatalf("deleted line missing: %v", d.Removed)
	}
	if _, err = repo.DiffLines(ctx, "--bad", "HEAD"); err == nil {
		t.Fatal("option accepted")
	}
	if _, err = repo.DiffLines(ctx, "HEAD", "--bad"); err == nil {
		t.Fatal("option accepted")
	}
}

func containsLine(ls []Line, want Line) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}

func TestParseLineDiffErrors(t *testing.T) {
	for _, in := range []string{
		"diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ nonsense @@\n",
		"diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,x +1 @@\n",
		"diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -x +1 @@\n",
		"diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1 +1 @@\ngarbage\n",
	} {
		if _, err := ParseLineDiff(in); err == nil {
			t.Fatalf("accepted %q", in)
		}
	}
	d, err := ParseLineDiff("diff --git a/q b/q\n--- \"a/q\\t\"\n+++ b/q\n@@ -1 +1 @@\n-a\n\\ No newline at end of file\n+b\n")
	if err != nil || len(d.Added) != 1 || len(d.Removed) != 1 {
		t.Fatalf("%+v %v", d, err)
	}
	if d, err = ParseLineDiff(""); err != nil || len(d.Added) != 0 {
		t.Fatal(d, err)
	}
}

func TestReviewHelpers(t *testing.T) {
	ctx := t.Context()
	repo, dir := fixture(ctx, t)
	base := head(ctx, t, repo)
	for _, n := range []string{"one", "two"} {
		writeFile(t, filepath.Join(dir, "source.txt"), n+"\n")
		git(ctx, t, dir, "commit", "-am", n+" subject\n\n"+n+" body")
	}
	diff, err := repo.DiffRange(ctx, base, "HEAD")
	if err != nil || !strings.Contains(diff, "-base") || !strings.Contains(diff, "+two") {
		t.Fatalf("%q %v", diff, err)
	}
	msgs, err := repo.Messages(ctx, base, "HEAD")
	if err != nil || len(msgs) != 2 || !strings.HasPrefix(msgs[0], "one subject") || !strings.HasSuffix(msgs[1], "two body") {
		t.Fatalf("%q %v", msgs, err)
	}
	if msgs, err = repo.Messages(ctx, "HEAD", "HEAD"); err != nil || len(msgs) != 0 {
		t.Fatal(msgs, err)
	}
	if s, err := repo.FirstSubject(ctx, base, "HEAD"); err != nil || s != "one subject" {
		t.Fatalf("%q %v", s, err)
	}
	if s, err := repo.FirstSubject(ctx, "HEAD", "HEAD"); err != nil || s != "" {
		t.Fatalf("%q %v", s, err)
	}
	for _, bad := range []string{"--bad", "nope"} {
		if _, err := repo.DiffRange(ctx, bad, "HEAD"); err == nil {
			t.Fatal("DiffRange accepted", bad)
		}
		if _, err := repo.DiffRange(ctx, "HEAD", bad); err == nil {
			t.Fatal("DiffRange accepted", bad)
		}
		if _, err := repo.Messages(ctx, bad, "HEAD"); err == nil {
			t.Fatal("Messages accepted", bad)
		}
		if _, err := repo.Messages(ctx, "HEAD", bad); err == nil {
			t.Fatal("Messages accepted", bad)
		}
		if _, err := repo.FirstSubject(ctx, bad, "HEAD"); err == nil {
			t.Fatal("FirstSubject accepted", bad)
		}
		if _, err := repo.FirstSubject(ctx, "HEAD", bad); err == nil {
			t.Fatal("FirstSubject accepted", bad)
		}
	}
}

func TestRemoteBranchSHAAndPushLease(t *testing.T) {
	ctx := t.Context()
	repo, dir := fixture(ctx, t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(root, "remote.git")
	git(ctx, t, root, "init", "-q", "--bare", bare)
	// Isolate from user config: netGit keeps the environment.
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	first := head(ctx, t, repo)

	if got, err := repo.RemoteBranchSHA(ctx, bare, "main"); err != nil || got != "" {
		t.Fatalf("missing branch: %q %v", got, err)
	}
	if err := repo.PushLease(ctx, bare, "main", first, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := repo.RemoteBranchSHA(ctx, bare, "main"); err != nil || got != first {
		t.Fatalf("%q %v", got, err)
	}
	writeFile(t, filepath.Join(dir, "source.txt"), "next\n")
	git(ctx, t, dir, "commit", "-am", "next")
	second := head(ctx, t, repo)
	if err := repo.PushLease(ctx, bare, "main", second, ""); err == nil {
		t.Fatal("stale lease accepted")
	}
	if err := repo.PushLease(ctx, bare, "main", second, first); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.RemoteBranchSHA(ctx, bare, "main"); got != second {
		t.Fatalf("remote at %q", got)
	}
	for _, bad := range []string{"", "-x", "a b", "a:b"} {
		if _, err := repo.RemoteBranchSHA(ctx, bad, "main"); err == nil {
			t.Fatal("remote accepted", bad)
		}
		if _, err := repo.RemoteBranchSHA(ctx, bare, bad); err == nil {
			t.Fatal("branch accepted", bad)
		}
		if err := repo.PushLease(ctx, bare, bad, second, ""); err == nil {
			t.Fatal("push branch accepted", bad)
		}
	}
	if err := repo.PushLease(ctx, bare, "main", "main", second); err == nil {
		t.Fatal("non-OID accepted")
	}
	if _, err := repo.RemoteBranchSHA(ctx, filepath.Join(root, "absent.git"), "main"); err == nil {
		t.Fatal("unreachable remote accepted")
	}
}
