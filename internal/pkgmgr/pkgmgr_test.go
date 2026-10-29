package pkgmgr

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func touch(t *testing.T, dir, name string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallCommand(t *testing.T) {
	d, _ := filepath.EvalSymlinks(t.TempDir())
	if InstallCommand(d) != nil {
		t.Fatal("no lockfile should give nil")
	}
	touch(t, d, "yarn.lock")
	if got := InstallCommand(d); !slices.Equal(got, []string{"yarn", "install", "--immutable"}) {
		t.Fatal(got)
	}
}

func TestNestedAndInstallIn(t *testing.T) {
	d, _ := filepath.EvalSymlinks(t.TempDir())
	if _, _, ok := Nested(d); ok {
		t.Fatal("none")
	}
	touch(t, d, "web/bun.lock")
	sub, m, ok := Nested(d)
	if !ok || sub != "web" || !slices.Equal(m.InstallIn(sub), []string{"bun", "install", "--cwd", "web", "--frozen-lockfile"}) {
		t.Fatal(sub, m, ok)
	}
	if got := InstallCommand(d); !slices.Equal(got, []string{"bun", "install", "--cwd", "web", "--frozen-lockfile"}) {
		t.Fatal("single nested lockfile should give its install:", got)
	}
	touch(t, d, "api/pnpm-lock.yaml")
	if _, _, ok := Nested(d); ok {
		t.Fatal("two nested lockfiles are ambiguous")
	}
	if _, _, ok := Nested(filepath.Join(d, "missing")); ok {
		t.Fatal("unreadable")
	}
	pn, _ := Detect(func() string { touch(t, d, "pnpm/pnpm-lock.yaml"); return filepath.Join(d, "pnpm") }())
	if !slices.Equal(pn.InstallIn("x"), []string{"pnpm", "install", "--dir", "x", "--frozen-lockfile"}) {
		t.Fatal(pn.InstallIn("x"))
	}
	npm := Managers[0]
	if !slices.Equal(npm.InstallIn("x"), []string{"npm", "ci", "--prefix", "x"}) {
		t.Fatal(npm.InstallIn("x"))
	}
}

func TestRunScript(t *testing.T) {
	want := map[string]string{
		"package-lock.json": "npm run --prefix web x", "pnpm-lock.yaml": "pnpm --dir web run x",
		"bun.lock": "bun run --cwd web x", "yarn.lock": "yarn --cwd web run x",
	}
	for _, m := range Managers {
		if w, ok := want[m.Lock]; ok && m.RunScript("web", "x") != w {
			t.Fatal(m.Lock, m.RunScript("web", "x"))
		}
		if m.RunScript("", "x") != m.Run+" x" {
			t.Fatal(m.RunScript("", "x"))
		}
	}
}

func TestPackageAndProject(t *testing.T) {
	d, _ := filepath.EvalSymlinks(t.TempDir())
	if p, err := ReadPackage(d); p != nil || err != nil {
		t.Fatal(p, err)
	}
	touch(t, d, "web/bun.lock")
	if err := os.WriteFile(filepath.Join(d, "web", "package.json"), []byte(`{"devDependencies":{"oxlint":"1.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sub, m := Project(d)
	p, err := ReadPackage(filepath.Join(d, sub))
	if err != nil || sub != "web" || m.Lock != "bun.lock" || !p.Declares("oxlint") || p.Declares("x") {
		t.Fatal(sub, m, p, err)
	}
	if got := m.RunScript(sub, "lint"); got != "bun run --cwd web lint" {
		t.Fatal(got)
	}
	if err := os.WriteFile(filepath.Join(d, "web", "package.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadPackage(filepath.Join(d, "web")); err == nil {
		t.Fatal("expected parse error")
	}
	if sub, _ := Project(t.TempDir()); sub != "" {
		t.Fatal(sub)
	}
}
