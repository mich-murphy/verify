package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/gitx"
)

func TestConfigNamespace(t *testing.T) {
	if FileName != "verify.yaml" {
		t.Fatalf("config filename %q", FileName)
	}
	dir := t.TempDir()
	cgit(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "gate.yaml"), []byte("bogus: legacy config must be ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, dir, "add", ".")
	cgit(t, dir, "commit", "-qm", "legacy config only")
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(t.Context(), repo, "HEAD")
	if err != nil || loaded.Found || loaded.Config.Base != "main" {
		t.Fatalf("legacy config must not govern Verify: %+v %v", loaded, err)
	}
}

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte(""))
	if err != nil || c.Base != "" || len(c.Prepare) != 0 {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestParseFull(t *testing.T) {
	c, err := Parse([]byte(`
base: develop
prepare:
  - {name: deps, run: [bun, install]}
verify:
  scenarios:
    - name: help
      run: [x, --help]
waivers:
  - {obligation: deadcode, reason: later}
`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Prepare[0].Timeout.Std().Minutes() != 10 || c.Verify.Scenarios[0].Timeout.Std().Seconds() != 60 || !c.Waived("deadcode") || c.Waived("x") {
		t.Fatalf("%+v", c)
	}
}

func TestParseRejects(t *testing.T) {
	bad := map[string]string{
		"unknown key":               "bogus: 1\n",
		"version removed":           "version: 1\n",
		"profile removed":           "profile: go\n",
		"checks removed":            "checks:\n  - {name: a, run: [a]}\n",
		"review removed":            "review:\n  reviewer: auto\n",
		"bad base":                  "base: -x\n",
		"prep no name":              "prepare:\n  - run: [a]\n",
		"prep no run":               "prepare:\n  - name: a\n",
		"bad duration":              "prepare:\n  - {name: a, run: [a], timeout: soon}\n",
		"bad glob":                  "signoff_paths: [\"a/[\"]\n",
		"waiver reason":             "waivers:\n  - {obligation: a}\n",
		"scenario exit":             "verify:\n  scenarios:\n    - {name: a, run: [a], exit: 300}\n",
		"not yaml":                  "base: [",
		"second document":           "base: main\n---\nbase: develop\n",
		"empty second document":     "base: main\n---\n",
		"surface without scenarios": "verify:\n  surfaces: ['cmd/**']\n",
		"empty setup command":       "verify:\n  setup: ['', arg]\n",
		"empty run command":         "verify:\n  scenarios:\n    - {name: help, run: ['', arg]}\n",
		"empty prepare command":     "prepare:\n  - {name: deps, run: ['', arg]}\n",
	}
	for name, in := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(in)); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestEmptyArgvArgumentsAreValid(t *testing.T) {
	c, err := Parse([]byte("prepare:\n  - {name: deps, run: [printf, '%s', '']}\nverify:\n  setup: [printf, '%s', '']\n  scenarios:\n    - {name: help, run: [printf, '%s', '']}\n"))
	if err != nil || len(c.Prepare[0].Run) != 3 || c.Prepare[0].Run[2] != "" || c.Verify.Setup[2] != "" || c.Verify.Scenarios[0].Run[2] != "" {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestParseEmptyOutputAssertions(t *testing.T) {
	c, err := Parse([]byte("verify:\n  surfaces: ['cmd/**']\n  scenarios:\n    - name: help\n      run: [verify]\n      stdout_equals: ''\n      stdout_contains: ''\n"))
	if err != nil || c.Verify.Scenarios[0].StdoutEquals == nil || *c.Verify.Scenarios[0].StdoutEquals != "" || c.Verify.Scenarios[0].StdoutContains == nil || *c.Verify.Scenarios[0].StdoutContains != "" {
		t.Fatalf("%+v %v", c, err)
	}
	c, err = Parse([]byte("verify:\n  scenarios:\n    - {name: help, run: [verify]}\n"))
	if err != nil || c.Verify.Scenarios[0].StdoutEquals != nil || c.Verify.Scenarios[0].StdoutContains != nil {
		t.Fatalf("%+v %v", c, err)
	}
}

func TestPrekConfig(t *testing.T) {
	d := t.TempDir()
	if p, err := PrekConfig(d); p != "" || err != nil {
		t.Fatalf("absent: %q %v", p, err)
	}
	for _, name := range []string{".pre-commit-config.yml", ".pre-commit-config.yaml", "prek.toml"} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		p, err := PrekConfig(d)
		if err != nil || p != filepath.Join(d, name) || !filepath.IsAbs(p) {
			t.Fatalf("%s: %q %v", name, p, err)
		}
	}
	if err := os.Remove(filepath.Join(d, "prek.toml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(d, "prek.toml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := PrekConfig(d); err == nil {
		t.Fatal("non-file config must fail instead of falling back")
	}
	if _, err := PrekConfig(filepath.Join(d, "missing")); err == nil {
		t.Fatal("nonexistent directory must fail")
	}
}

func TestLoadTrustedBase(t *testing.T) {
	d := t.TempDir()
	cgit(t, d, "init", "-q", "-b", "main")
	write := func(value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(d, "verify.yaml"), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("signoff_paths: ['trusted/**']\n")
	cgit(t, d, "add", ".")
	cgit(t, d, "commit", "-qm", "trusted config")
	cgit(t, d, "checkout", "-qb", "feature")
	write("base: feature\nsignoff_paths: ['candidate/**']\n")
	cgit(t, d, "commit", "-qam", "candidate self ref")
	repo, err := gitx.Open(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := repo.Resolve(t.Context(), "main")
	l, err := Load(t.Context(), repo, "HEAD")
	if err != nil || l.MergeBase != base || l.FromCandidate || !strings.HasPrefix(l.Config.SignoffPaths[0], "trusted") {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestLoadRejectsCandidateSelfBaseButAllowsAlternate(t *testing.T) {
	d := t.TempDir()
	cgit(t, d, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(d, "a"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, d, "add", ".")
	cgit(t, d, "commit", "-qm", "root")
	cgit(t, d, "branch", "alternate")
	cgit(t, d, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(d, "verify.yaml"), []byte("base: alternate\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, d, "add", ".")
	cgit(t, d, "commit", "-qm", "alternate base")
	repo, err := gitx.Open(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := repo.Resolve(t.Context(), "main")
	l, err := Load(t.Context(), repo, "HEAD")
	if err != nil || l.MergeBase != base || !l.FromCandidate || l.Config.Base != "alternate" {
		t.Fatalf("%+v %v", l, err)
	}
	if err := os.WriteFile(filepath.Join(d, "verify.yaml"), []byte("base: feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, d, "commit", "-qam", "self base")
	if _, err := Load(t.Context(), repo, "HEAD"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self base: %v", err)
	}
}

func TestLoadWithoutDefaultRefRequiresIndependentBase(t *testing.T) {
	d := t.TempDir()
	cgit(t, d, "init", "-q", "-b", "trunk")
	if err := os.WriteFile(filepath.Join(d, "a"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, d, "add", ".")
	cgit(t, d, "commit", "-qm", "root")
	cgit(t, d, "checkout", "-qb", "feature")
	if err := os.WriteFile(filepath.Join(d, "verify.yaml"), []byte("base: trunk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, d, "add", ".")
	cgit(t, d, "commit", "-qm", "feature")
	repo, err := gitx.Open(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	base, _ := repo.Resolve(t.Context(), "trunk")
	l, err := Load(t.Context(), repo, "HEAD")
	if err != nil || l.MergeBase != base || !l.FromCandidate {
		t.Fatalf("explicit independent base: %+v %v", l, err)
	}
	if err := os.WriteFile(filepath.Join(d, "verify.yaml"), []byte("base: feature\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, d, "commit", "-qam", "self")
	if _, err := Load(t.Context(), repo, "HEAD"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("self without anchor: %v", err)
	}
	cgit(t, d, "checkout", "-q", "trunk")
	if err := os.WriteFile(filepath.Join(d, "verify.yaml"), []byte("base: trunk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, d, "add", ".")
	cgit(t, d, "commit", "-qm", "sole self")
	if _, err := Load(t.Context(), repo, "HEAD"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("sole self without anchor: %v", err)
	}
}

func cgit(t *testing.T, dir string, args ...string) {
	t.Helper()
	c := exec.CommandContext(t.Context(), "git", args...) // #nosec G204 -- test helper with fixed executable
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestLoad(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	w := func(n, c string) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cgit(t, dir, "init", "-q", "-b", "main")
	w("a", "1")
	cgit(t, dir, "add", ".")
	cgit(t, dir, "commit", "-qm", "base")
	cgit(t, dir, "checkout", "-qb", "feat")
	w("verify.yaml", "signoff_paths: [\"x/**\"]\n")
	cgit(t, dir, "add", ".")
	cgit(t, dir, "commit", "-qm", "add config")
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := Load(t.Context(), repo, "feat")
	if err != nil || !l.FromCandidate || !l.Found || len(l.Config.SignoffPaths) != 1 || l.Config.Base != "main" {
		t.Fatalf("%+v %v", l, err)
	}
	l, err = Load(t.Context(), repo, "main")
	if err != nil || l.Found || l.FromCandidate || l.Config.Base != "main" {
		t.Fatalf("%+v %v", l, err)
	}
	cgit(t, dir, "checkout", "-q", "main")
	cgit(t, dir, "merge", "-q", "feat")
	w("verify.yaml", "signoff_paths: [\"y/**\"]\n")
	cgit(t, dir, "commit", "-qam", "tweak")
	l, err = Load(t.Context(), repo, "HEAD")
	if err != nil || l.FromCandidate || l.Config.SignoffPaths[0] != "y/**" {
		t.Fatalf("%+v %v", l, err)
	}
	w("verify.yaml", "bogus: 1\n")
	cgit(t, dir, "commit", "-qam", "bad")
	if _, err = Load(t.Context(), repo, "HEAD"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid, got %v", err)
	}
	if _, err = Load(t.Context(), repo, "nope"); err == nil {
		t.Fatal("want error")
	}
}

// An old-format merge-base copy (version:) does not parse: the candidate's
// copy governs and the change is Sign-off; with no candidate copy, defaults.
func TestLoadInvalidMergeBaseFallsBack(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	w := func(n, c string) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cgit(t, dir, "init", "-q", "-b", "main")
	w("verify.yaml", "version: 1\nprofile: go\n")
	cgit(t, dir, "add", ".")
	cgit(t, dir, "commit", "-qm", "base")
	cgit(t, dir, "checkout", "-qb", "feat")
	w("verify.yaml", "verify:\n  surfaces: [\"cmd/**\"]\n  scenarios:\n    - {name: help, run: [verify]}\n")
	cgit(t, dir, "commit", "-qam", "new format")
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	l, err := Load(t.Context(), repo, "feat")
	if err != nil || !l.FromCandidate || !l.Found || len(l.Config.Verify.Surfaces) != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	cgit(t, dir, "rm", "-q", "verify.yaml")
	cgit(t, dir, "commit", "-qm", "delete")
	l, err = Load(t.Context(), repo, "feat")
	if err != nil || !l.FromCandidate || l.Found || l.Config.Base != "main" {
		t.Fatalf("deleted candidate: %+v %v", l, err)
	}
}

// A local base branch behind origin/<base> must not pull already-merged
// commits into the change: the newer merge-base wins.
func TestLoadStaleLocalBaseUsesOrigin(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	w := func(n, c string) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(c), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cgit(t, dir, "init", "-q", "-b", "main")
	w("a", "1")
	cgit(t, dir, "add", ".")
	cgit(t, dir, "commit", "-qm", "base")
	w("a", "2")
	cgit(t, dir, "commit", "-qam", "merged upstream")
	cgit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	cgit(t, dir, "checkout", "-qb", "feat")
	w("b", "1")
	cgit(t, dir, "add", ".")
	cgit(t, dir, "commit", "-qm", "feature")
	cgit(t, dir, "branch", "-f", "main", "HEAD~2") // local main is stale
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := repo.Resolve(t.Context(), "origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if l, err := Load(t.Context(), repo, "feat"); err != nil || l.MergeBase != want {
		t.Fatalf("merge-base %s want %s (%v)", l.MergeBase, want, err)
	}
	// Local main ahead of origin/main: the local merge-base is newer.
	cgit(t, dir, "branch", "-f", "main", "feat")
	cgit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD~2")
	head, _ := repo.Resolve(t.Context(), "feat")
	if l, err := Load(t.Context(), repo, "feat"); err != nil || l.MergeBase != head {
		t.Fatalf("merge-base %s want %s (%v)", l.MergeBase, head, err)
	}
}

func TestLoadDerivesBaseFromOriginHead(t *testing.T) {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	cgit(t, dir, "init", "-q", "-b", "develop")
	if err := os.WriteFile(filepath.Join(dir, "a"), []byte("1"), 0o600); err != nil {
		t.Fatal(err)
	}
	cgit(t, dir, "add", ".")
	cgit(t, dir, "commit", "-qm", "base")
	cgit(t, dir, "update-ref", "refs/remotes/origin/develop", "HEAD")
	cgit(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	repo, err := gitx.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if l, err := Load(t.Context(), repo, "HEAD"); err != nil || l.Config.Base != "develop" {
		t.Fatalf("%+v %v", l, err)
	}
}
