package testenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) { os.Exit(Main(m)) }

func TestMainPointsHomeConfigAndTmpdirAtOneFreshRealDirectory(t *testing.T) {
	home := os.Getenv("HOME")
	if home == "" {
		t.Fatal("HOME unset")
	}
	if real, err := filepath.EvalSymlinks(home); err != nil || real != home {
		t.Fatalf("HOME %q is not a real path (%q, %v)", home, real, err)
	}
	if got := os.Getenv("XDG_CONFIG_HOME"); got != filepath.Join(home, ".config") {
		t.Fatalf("XDG_CONFIG_HOME %q", got)
	}
	tmp := os.Getenv("TMPDIR")
	if !strings.HasPrefix(tmp, home+string(filepath.Separator)) {
		t.Fatalf("TMPDIR %q is not under HOME %q", tmp, home)
	}
	if fi, err := os.Stat(tmp); err != nil || !fi.IsDir() { //nolint:gosec // path set by testenv.Main above
		t.Fatalf("TMPDIR %q does not exist: %v", tmp, err)
	}
}
