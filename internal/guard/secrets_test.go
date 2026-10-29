package guard

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/state"
)

// fakePAT builds a GitHub-PAT-shaped string at runtime so the source file
// itself does not trip secret scanners.
func fakePAT() string {
	return "gh" + "p_" + "aB3dE5fG7hJ9kL1mN3pQ5rS7tU9vW1xY3zA5"
}

func secretsRepo(t *testing.T, files map[string]string) (*gitx.Repo, string, string) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	git(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "README.md", "hello\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	for n, b := range files {
		put(t, dir, n, b)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "change")
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return repo, rev(t, repo, "HEAD~1"), rev(t, repo, "HEAD")
}

func TestSecretsFound(t *testing.T) {
	tok := fakePAT()
	repo, base, head := secretsRepo(t, map[string]string{"cfg.txt": "# c\ntoken = " + tok + "\n"})
	fs, err := scanSecrets(t.Context(), Input{Repo: repo, Base: base, SHA: head, Config: config.Default()})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Path != "cfg.txt" || fs[0].Line != 2 || fs[0].Severity != "block" {
		t.Fatalf("%+v", fs)
	}
	for _, f := range fs {
		if strings.Contains(f.Message, tok) || strings.Contains(f.Message, tok[4:]) || strings.Contains(f.ID, tok) {
			t.Fatal("secret leaked into finding")
		}
	}
}

func TestSecretsRepoAllowlistIgnored(t *testing.T) {
	tok := fakePAT()
	repo, base, head := secretsRepo(t, map[string]string{
		"cfg.txt":         "token = " + tok + " # gitleaks:allow\n",
		".gitleaks.toml":  "[allowlist]\npaths = ['''cfg\\.txt''']\n",
		".gitleaksignore": "x\n",
	})
	fs, err := scanSecrets(t.Context(), Input{Repo: repo, Base: base, SHA: head})
	if err != nil || len(fs) != 1 {
		t.Fatalf("%+v %v", fs, err)
	}
}

func TestSecretsClean(t *testing.T) {
	repo, base, head := secretsRepo(t, map[string]string{"a.txt": "nothing here\n"})
	fs, err := scanSecrets(t.Context(), Input{Repo: repo, Base: base, SHA: head})
	if err != nil || len(fs) != 0 {
		t.Fatalf("%+v %v", fs, err)
	}
}

func TestSecretsCanceled(t *testing.T) {
	repo, base, head := secretsRepo(t, map[string]string{"a.txt": "x\n"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := scanSecrets(ctx, Input{Repo: repo, Base: base, SHA: head}); err == nil {
		t.Fatal("want error")
	}
}

func scanRange(t *testing.T, dir, base string) ([]state.Finding, error) {
	t.Helper()
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	return scanSecrets(t.Context(), Input{Repo: repo, Base: base, SHA: rev(t, repo, "HEAD")})
}

func TestSecretsAddedThenRemovedStillFound(t *testing.T) {
	tok := fakePAT()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	git(t, dir, "init", "-q", "-b", "main")
	put(t, dir, "README.md", "hello\n")
	git(t, dir, "add", ".")
	git(t, dir, "commit", "-q", "-m", "base")
	base := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	put(t, dir, "cfg.txt", "# c\ntoken = "+tok+" # gitleaks:allow\n")
	put(t, dir, ".gitleaks.toml", "[allowlist]\npaths = ['''cfg\\.txt''']\n")
	put(t, dir, ".betterleaksignore", "x\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "add")
	first := strings.TrimSpace(gitOut(t, dir, "rev-parse", "HEAD"))
	put(t, dir, "cfg.txt", "# c\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "remove")

	// Bogus inherited git variables must not redirect the scan.
	t.Setenv("GIT_DIR", filepath.Join(dir, "nope"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(dir, "nope-index"))
	fs, err := scanRange(t, dir, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Path != "cfg.txt" || fs[0].Line != 2 || !strings.Contains(fs[0].Message, first[:12]) {
		t.Fatalf("%+v want cfg.txt:2 in %s", fs, first[:12])
	}
}

// TestSecretsBuiltinPrefilterApplied checks that the built-in rules' global
// prefilter still skips go.sum, while the same line elsewhere is flagged (so
// the test is not vacuous). The line is assembled at runtime so this file does
// not trip secret scanners.
func TestSecretsBuiltinPrefilterApplied(t *testing.T) {
	line := "github.com/pkoukk/tiktoken-go v0.1.8 h1:" + "85ENo+3FpWgAACBaEUVp" + "+lctuTcYUO7BtmfhlN/QTRo=\n"
	repo, base, head := secretsRepo(t, map[string]string{"go.sum": line, "notes.txt": line})
	fs, err := scanSecrets(t.Context(), Input{Repo: repo, Base: base, SHA: head})
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 1 || fs[0].Path != "notes.txt" {
		t.Fatalf("want only notes.txt flagged, got %+v", fs)
	}
}

func TestSecretsBadBaseErrors(t *testing.T) {
	repo, _, head := secretsRepo(t, map[string]string{"a.txt": "x\n"})
	if _, err := scanSecrets(t.Context(), Input{Repo: repo, Base: "no-such-ref", SHA: head}); err == nil {
		t.Fatal("want error")
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}

func TestFastSecretScanIncludesUntrackedAndUnstaged(t *testing.T) {
	repo, _, sha := secretsRepo(t, map[string]string{"cfg.txt": "placeholder\n"})
	put(t, repo.Root, "cfg.txt", "token = "+fakePAT()+"\n")
	put(t, repo.Root, "new.txt", "token = "+fakePAT()+"\n")
	fs, err := Run(t.Context(), Input{Repo: repo, Base: sha, SHA: sha, Fast: true})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range fs {
		if f.Rule == "guard:secrets" {
			seen[f.Path] = true
		}
	}
	if !seen["cfg.txt"] || !seen["new.txt"] {
		t.Fatalf("fast secret bypass: %+v", fs)
	}
}
