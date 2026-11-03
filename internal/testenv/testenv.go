// Package testenv isolates a test binary from the developer's home directory.
package testenv

import (
	"os"
	"path/filepath"
	"testing"
)

// Main runs a package's tests with HOME, XDG_CONFIG_HOME and TMPDIR pointing at a
// fresh real-path directory and unsets the variables git exports to hooks from a
// linked worktree, which would send a test's git commands at the real repository. Call it from TestMain: os.Exit(testenv.Main(m)).
// Without it, a symlinked global git config or ignore file, or a /var to
// /private/var TMPDIR, changes what the tests see.
func Main(m *testing.M) int {
	dir, err := os.MkdirTemp("", "verify-test-home-")
	if err == nil {
		dir, err = filepath.EvalSymlinks(dir)
	}
	if err != nil {
		panic(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	tmp := filepath.Join(dir, "tmp")
	if err := os.Mkdir(tmp, 0o700); err != nil {
		panic(err)
	}
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE"} {
		if err := os.Unsetenv(k); err != nil {
			panic(err)
		}
	}
	for k, v := range map[string]string{"HOME": dir, "XDG_CONFIG_HOME": filepath.Join(dir, ".config"), "TMPDIR": tmp} {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}
	return m.Run()
}
