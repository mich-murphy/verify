package gitx_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
)

// Exercise both policy reads without giving config tests/files to this worker.
func TestBattleSymlinkPolicyFailsClosed(t *testing.T) {
	for _, trusted := range []bool{false, true} {
		name := "candidate"
		if trusted {
			name = "trusted"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			git := func(args ...string) {
				t.Helper()
				cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...) // #nosec G204 -- Fixed Git and fixture-owned argv/repository.
				cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + dir, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
			}
			put := func(name, body string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			policy := filepath.Join(dir, config.FileName)
			git("init", "-q", "-b", "main")
			put("policy-target", "base: main\n")
			if trusted {
				if err := os.Symlink("policy-target", policy); err != nil {
					t.Fatal(err)
				}
			}
			git("add", ".")
			git("commit", "-qm", "base")
			git("checkout", "-qb", "feature")
			if trusted {
				if err := os.Remove(policy); err != nil {
					t.Fatal(err)
				}
				put(config.FileName, "base: main\n")
			} else if err := os.Symlink("policy-target", policy); err != nil {
				t.Fatal(err)
			}
			git("add", ".")
			git("commit", "-qm", "candidate")
			r, err := gitx.Open(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			sha, err := r.Head(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(t.Context(), r, sha); !errors.Is(err, gitx.ErrNonRegularFile) {
				t.Fatalf("%s symlink policy must fail closed: %v", name, err)
			}
			revision := "HEAD"
			if trusted {
				revision = "main"
			}
			if _, found, err := r.FileAt(t.Context(), revision, config.FileName); found || !errors.Is(err, gitx.ErrNonRegularFile) {
				t.Fatalf("existing nonregular policy: found=%v err=%v", found, err)
			}
			if _, found, err := r.FileAt(t.Context(), revision, "absent.yaml"); found || err != nil {
				t.Fatalf("absent policy: found=%v err=%v", found, err)
			}
		})
	}
}
