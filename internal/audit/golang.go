package audit

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

var coreLinters = []string{"govet", "staticcheck", "errcheck", "errorlint", "ineffassign", "unused", "gosec", "revive", "gocritic", "nolintlint", "gocognit"}

var standardLinters = []string{"errcheck", "govet", "ineffassign", "staticcheck", "unused"}

var goDirective = regexp.MustCompile(`(?m)^go\s+1\.\d+`)

type golangciFile struct {
	Version string `yaml:"version"`
	Linters struct {
		Default  string   `yaml:"default"`
		Enable   []string `yaml:"enable"`
		Disable  []string `yaml:"disable"`
		Settings struct {
			Nolintlint struct {
				RequireExplanation bool `yaml:"require-explanation"`
			} `yaml:"nolintlint"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

// golangciProblem describes what is wrong with .golangci.yml, or "".
func (a *auditor) golangciProblem() string {
	text, ok := a.read(".golangci.yml")
	if !ok {
		text, ok = a.read(".golangci.yaml")
	}
	if !ok {
		return "no .golangci.yml found"
	}
	var g golangciFile
	if err := yaml.Unmarshal([]byte(text), &g); err != nil {
		return "cannot parse .golangci.yml: " + err.Error()
	}
	var base []string
	switch g.Linters.Default {
	case "", "standard":
		base = standardLinters
	case "all":
		base = coreLinters
	}
	active := slices.Concat(base, g.Linters.Enable)
	missing := slices.DeleteFunc(slices.Clone(coreLinters), func(l string) bool {
		return slices.Contains(active, l) && !slices.Contains(g.Linters.Disable, l)
	})
	switch {
	case g.Version != "2":
		return `.golangci.yml must declare version: "2"`
	case len(missing) > 0:
		return "golangci-lint config lacks: " + strings.Join(missing, ", ")
	case !g.Linters.Settings.Nolintlint.RequireExplanation:
		return "nolintlint needs settings.require-explanation: true"
	}
	return ""
}

func (a *auditor) goFiles() {
	a.add("golangci-config", Required, a.golangciProblem(),
		"use `version: \"2\"` with linters.enable: ["+strings.Join(coreLinters, ", ")+"] and linters.settings.nolintlint.require-explanation: true")
	mod, _ := a.read("go.mod")
	a.add("go-version", Required, problemIf(!goDirective.MatchString(mod), "go.mod has no pinned go directive"),
		"add a `go 1.NN.P` line (and optionally `toolchain goX.Y.Z`) to go.mod")
	a.add("fuzz", Recommended, problemIf(!a.hasFuzz(), "no fuzz targets"), "add `func FuzzXxx(f *testing.F)` for parsers and decoders")
}

// problemIf returns msg when bad, else "".
func problemIf(bad bool, msg string) string {
	if bad {
		return msg
	}
	return ""
}

func (a *auditor) hasFuzz() bool {
	found := false
	_ = filepath.WalkDir(a.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return nil //nolint:nilerr // unreadable entries are skipped; the walk is only a recommendation signal
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "node_modules" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, "_test.go") {
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), "\nfunc Fuzz") { //nolint:gosec // G122: read-only recommendation signal; no write or follow-up on the path
				found = true
			}
		}
		return nil
	})
	return found
}
