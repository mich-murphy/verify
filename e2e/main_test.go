package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mich-murphy/verify/internal/testenv"
)

// goEnv holds the real Go cache locations. testenv.Main moves HOME, so without
// them every hook would compile the standard library from scratch.
var goEnv []string

func TestMain(m *testing.M) {
	for _, k := range []string{"GOCACHE", "GOMODCACHE"} {
		out, err := exec.CommandContext(context.Background(), "go", "env", k).Output() // #nosec G204 -- fixed command
		if err != nil {
			panic(err)
		}
		goEnv = append(goEnv, k+"="+strings.TrimSpace(string(out)))
	}
	os.Exit(testenv.Main(m))
}

var (
	buildOnce sync.Once
	binPath   string
	buildErr  error
)

// binary builds cmd/verify once per test run, skipping when prek is missing.
func binary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("prek"); err != nil {
		unavailable(t, "prek not on PATH")
	}
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "verify-e2e-bin-")
		if err != nil {
			buildErr = err
			return
		}
		binPath = filepath.Join(dir, "verify")
		c := exec.CommandContext(t.Context(), "go", "build", "-o", binPath, "./cmd/verify") // #nosec G204 -- fixed command
		c.Dir = ".."
		c.Env = append(os.Environ(), goEnv...)
		if out, err := c.CombinedOutput(); err != nil {
			buildErr = &buildFailure{err, string(out)}
		}
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	return binPath
}

type buildFailure struct {
	err error
	out string
}

func (b *buildFailure) Error() string { return "go build: " + b.err.Error() + "\n" + b.out }

// unavailable skips locally but fails when CI is set, so CI cannot silently skip the package.
func unavailable(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}
