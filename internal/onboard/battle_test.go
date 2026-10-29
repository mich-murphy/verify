package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mich-murphy/verify/internal/pkgmgr"
)

func TestBattleNestedStarterDirectoryWithSpaces(t *testing.T) {
	d := tmp(t, map[string]string{"web app/package-lock.json": "{}", "web app/package.json": `{"devDependencies":{"typescript":"1.0.0"}}`})
	sub, m := pkgmgr.Project(d)
	pkg, err := pkgmgr.ReadPackage(filepath.Join(d, sub))
	if err != nil {
		t.Fatal(err)
	}
	var res Result
	entry, _, ok := starterEntry("", true, tsDefs["typecheck"], pkg, m, sub, "typecheck", "pre-push", &res)
	if !ok {
		t.Fatal(res)
	}
	// Replace the tool invocation with a shell builtin; this exercises only the
	// generated cd/quoting, with no package-manager execution or network access.
	entry = strings.ReplaceAll(entry, "npx tsc --noEmit", "true")
	cmd := exec.CommandContext(t.Context(), "sh", "-c", entry) // #nosec G204 -- generated fixture command; tool invocation replaced with true
	cmd.Dir = d
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated nested command %q failed: %v: %s", entry, err, out)
	}
}

func TestBattleApplyTwicePreservesBytes(t *testing.T) {
	fakePrek(t)
	original := "# preserve comments and order\nrepos:\n  - repo: local\n    hooks:\n      - {id: custom, name: custom, entry: true, language: system}\n"
	d := tmp(t, map[string]string{"go.mod": "module fixture\n\ngo 1.27.0\n", prekFile: original})
	_, first := apply(t, d)
	_, second := apply(t, d)
	if !strings.HasPrefix(first, original) || first != second {
		t.Fatalf("apply did not preserve prefix/idempotence:\nfirst=%s\nsecond=%s", first, second)
	}
}

func BenchmarkBattlePlan(b *testing.B) {
	text := "# preserve\nrepos:\n  - repo: local\n    hooks:\n      - {id: lint, name: lint, entry: true, language: system}\n"
	b.ReportAllocs()
	for b.Loop() {
		plan(text)
	}
}

func TestBattlePrekQuotedNestedCommands(t *testing.T) {
	if _, err := exec.LookPath("prek"); err != nil {
		t.Skip("prek missing")
	}
	for _, sub := range []string{"web app", "web'app", "web; literal"} {
		t.Run(sub, func(t *testing.T) {
			d := tmp(t, map[string]string{sub + "/package-lock.json": "{}", sub + "/package.json": `{"scripts":{"lint":"true"},"devDependencies":{"typescript":"1.0.0"}}`, "bin/npm": "#!/bin/sh\n[ \"$1\" = run ] && [ \"$2\" = --prefix ] && [ -d \"$3\" ] && [ \"$4\" = lint ] && [ \"$#\" -eq 4 ]\n", "bin/npx": "#!/bin/sh\n[ -f package.json ] && [ \"$1\" = tsc ] && [ \"$2\" = --noEmit ]\n"})
			for _, name := range []string{"npm", "npx"} {
				if err := os.Chmod(filepath.Join(d, "bin", name), 0o700); err != nil {
					t.Fatal(err)
				}
			} // #nosec G302 -- private executable fixtures
			t.Setenv("PATH", filepath.Join(d, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
			git := exec.CommandContext(t.Context(), "git", "init", "-q")
			git.Dir = d
			if out, err := git.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			m := pkgmgr.Managers[0]
			pkg, err := pkgmgr.ReadPackage(filepath.Join(d, sub))
			if err != nil {
				t.Fatal(err)
			}
			var res Result
			starter, _, _ := starterEntry("", true, tsDefs["typecheck"], pkg, m, sub, "typecheck", "pre-push", &res)
			for _, entry := range []string{m.RunScript(sub, "lint"), starter} {
				text := "repos:\n  - repo: local\n    hooks:\n      - id: probe\n        name: probe\n        entry: " + strconv.Quote(entry) + "\n        language: system\n        pass_filenames: false\n        always_run: true\n"
				if err := os.WriteFile(filepath.Join(d, prekFile), []byte(text), 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := exec.CommandContext(t.Context(), "prek", "run", "--all-files")
				cmd.Dir = d
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("entry %q: %v\n%s", entry, err, out)
				}
			}
		})
	}
}
