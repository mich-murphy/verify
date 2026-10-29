// Package pkgmgr is the single table of JavaScript package managers, keyed by
// lockfile. The setup audit, onboarding and the check's derived prepare step
// all read it.
package pkgmgr

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Manager is a JavaScript package manager, identified by its lockfile.
type Manager struct {
	Lock, Run, Exec string
	Install         []string // frozen-lockfile install at the lockfile's directory
	DirFlag         string   // flag that points Install at another directory
	RunIn           string   // format of the command running a script in another directory: dir, script
}

// Managers lists the supported managers in detection order.
var Managers = []Manager{
	{"package-lock.json", "npm run", "npx", []string{"npm", "ci"}, "--prefix", "npm run --prefix %s %s"},
	{"pnpm-lock.yaml", "pnpm run", "pnpm exec", []string{"pnpm", "install", "--frozen-lockfile"}, "--dir", "pnpm --dir %s run %s"},
	{"bun.lock", "bun run", "bunx", []string{"bun", "install", "--frozen-lockfile"}, "--cwd", "bun run --cwd %s %s"},
	{"bun.lockb", "bun run", "bunx", []string{"bun", "install", "--frozen-lockfile"}, "--cwd", "bun run --cwd %s %s"},
	{"yarn.lock", "yarn run", "yarn", []string{"yarn", "install", "--immutable"}, "--cwd", "yarn --cwd %s run %s"},
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Detect returns the manager whose lockfile is in dir.
func Detect(dir string) (Manager, bool) {
	for _, m := range Managers {
		if exists(filepath.Join(dir, m.Lock)) {
			return m, true
		}
	}
	return Manager{}, false
}

// InstallCommand returns the install command implied by dir's root lockfile,
// else by the single immediate subdirectory that holds one, or nil.
func InstallCommand(dir string) []string {
	if m, ok := Detect(dir); ok {
		return slices.Clone(m.Install)
	}
	if sub, m, ok := Nested(dir); ok {
		return m.InstallIn(sub)
	}
	return nil
}

// InstallIn returns the install command for the project in sub, a directory
// below the root: the manager's directory flag goes after the command name.
func (m Manager) InstallIn(sub string) []string {
	n := 2
	if m.Install[0] == "npm" {
		n = len(m.Install)
	}
	out := slices.Clone(m.Install[:n])
	out = append(out, m.DirFlag, sub)
	return append(out, m.Install[n:]...)
}

// RunScript returns the command that runs package script name in sub, a
// directory below the root ("" for the root itself).
func (m Manager) RunScript(sub, name string) string {
	if sub == "" {
		return m.Run + " " + QuoteArg(name)
	}
	return fmt.Sprintf(m.RunIn, QuoteArg(sub), QuoteArg(name))
}

// Nested finds the one immediate subdirectory of dir that holds a lockfile.
// It reports false when there is none or more than one.
func Nested(dir string) (sub string, m Manager, ok bool) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", Manager{}, false
	}
	n := 0
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || e.Name() == "node_modules" {
			continue
		}
		if found, has := Detect(filepath.Join(dir, e.Name())); has {
			sub, m = e.Name(), found
			n++
		}
	}
	return sub, m, n == 1
}

// Package is the part of package.json that Verify reads.
type Package struct {
	Scripts         map[string]string `json:"scripts"`
	Dependencies    map[string]string `json:"dependencies"`
	DevDependencies map[string]string `json:"devDependencies"`
	PackageManager  string            `json:"packageManager"`
}

// ReadPackage reads dir/package.json. It returns nil, nil when the file is absent.
func ReadPackage(dir string) (*Package, error) {
	b, err := os.ReadFile(filepath.Join(dir, "package.json")) // #nosec G304 -- reads repository files by design
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // absent is a normal answer, not an error
	}
	if err != nil {
		return nil, err
	}
	p := &Package{}
	if err := json.Unmarshal(b, p); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", filepath.Join(dir, "package.json"), err)
	}
	return p, nil
}

// Declares reports whether tool is a dependency or devDependency.
func (p *Package) Declares(tool string) bool {
	if p == nil {
		return false
	}
	_, dep := p.Dependencies[tool]
	_, dev := p.DevDependencies[tool]
	return dep || dev
}

// Project returns the JavaScript project the hooks run in: the root when it
// has a lockfile, else the single subdirectory that does (sub), else the root.
func Project(dir string) (sub string, m Manager) {
	if m, ok := Detect(dir); ok {
		return "", m
	}
	if sub, m, ok := Nested(dir); ok {
		return sub, m
	}
	return "", Managers[0]
}

// QuoteArg quotes one argument for prek entry splitting and POSIX shells.
func QuoteArg(s string) string {
	if s != "" && strings.Trim(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_./:@%+=,-") == "" {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}
