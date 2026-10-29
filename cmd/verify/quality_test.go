package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestQualityFileInventory(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git := func(args ...string) {
		t.Helper()
		// #nosec G204 -- Fixed git executable and fixture-only arguments.
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	for _, name := range []string{"deleted.go", "old.go", "tracked.go"} {
		write(name, "package fixture\n")
	}
	git("add", ".")
	if err := os.Remove(filepath.Join(dir, "deleted.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "old.go"), filepath.Join(dir, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	write("new.go", "package fixture\n")
	write("ignored.go", "package fixture\n")
	write(".gitignore", "ignored.go\n")
	write("inventory.mk", "test-inventory:\n\t@printf '%s\\n' $(GOFILES)\n")
	// #nosec G204 -- Fixed recipe and fixture makefile; no user-selected commands.
	cmd := exec.CommandContext(t.Context(), "make", "--no-print-directory", "-s", "-f", filepath.Join(root, "Makefile"), "-f", "inventory.mk", "test-inventory")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inventory: %v\n%s", err, out)
	}
	got := strings.Fields(string(out))
	slices.Sort(got)
	want := []string{"new.go", "renamed.go", "tracked.go"}
	if !slices.Equal(got, want) {
		t.Fatalf("Go file inventory %q, want %q", got, want)
	}
}

// The quality commands must propagate a tool failure, even with empty stdout.
// Running them with stub tools tests the shell recipes rather than Go's runner.
func TestQualityRecipes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"fmt-check", "deadcode", "hook-format"} {
		tools := []string{"gofumpt", "goimports"}
		if target == "deadcode" {
			tools = []string{"deadcode"}
		}
		for _, tool := range tools {
			for _, mode := range []string{"clean", "failure", "finding"} {
				t.Run(target+"/"+tool+"/"+mode, func(t *testing.T) {
					dir := t.TempDir()
					stub := filepath.Join(dir, "go")
					body := "#!/bin/sh\nfor arg do\n  if [ \"$arg\" = \"$TEST_TOOL\" ]; then\n    case \"$TEST_MODE\" in\n      failure) exit 9 ;;\n      finding) echo finding; exit 0 ;;\n    esac\n  fi\ndone\n"
					// #nosec G306 -- Private executable fixture under t.TempDir needs owner execute permission.
					if err := os.WriteFile(stub, []byte(body), 0o700); err != nil {
						t.Fatal(err)
					}
					var cmd *exec.Cmd
					if target == "hook-format" {
						// #nosec G204 -- Fixed repository script and fixture filename; no user input.
						cmd = exec.CommandContext(t.Context(), "/bin/sh", filepath.Join(root, "scripts/fmt-check.sh"), "sample.go")
					} else {
						// #nosec G204 -- Fixed recipe with a private test executable, not user-selected commands.
						cmd = exec.CommandContext(t.Context(), "make", "-f", filepath.Join(root, "Makefile"), target, "GOTOOL="+stub, "GOFILES=sample.go")
					}
					cmd.Dir = dir
					cmd.Env = append(os.Environ(), "TEST_TOOL="+tool, "TEST_MODE="+mode, "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
					output, err := cmd.CombinedOutput()
					if (err == nil) != (mode == "clean") {
						t.Fatalf("mode %s: err=%v, output=%s", mode, err, output)
					}
					if mode == "finding" && !strings.Contains(string(output), "finding") {
						t.Fatalf("finding not reported: %s", output)
					}
				})
			}
		}
	}
}

// Inspect recipes without running their tools: CI must retain the complete
// test contract while producing race and coverage evidence in one invocation.
func TestQualityTestRunRecipes(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		args     []string
		want     string
		coverage bool
	}{
		{"ci", []string{"ci"}, "go test -race -shuffle=on -count=1 -coverprofile=coverage.out ./...", true},
		{"cached hook coverage", []string{"cover", "TESTFLAGS="}, "go test -race -coverprofile=coverage.out ./...", true},
		{"standalone test", []string{"test"}, "go test -race -shuffle=on -count=1 ./...", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--no-print-directory", "-n", "-f", filepath.Join(root, "Makefile"), "GOFILES=sample.go"}, tc.args...)
			cmd := exec.CommandContext(t.Context(), "make", args...) // #nosec G204 -- fixed dry-run recipe and repository Makefile
			cmd.Dir = t.TempDir()
			// Do not let a developer's optional recipe override change this fixture.
			for _, entry := range os.Environ() {
				if !strings.HasPrefix(entry, "TESTFLAGS=") && !strings.HasPrefix(entry, "MAKEFLAGS=") && !strings.HasPrefix(entry, "MFLAGS=") && !strings.HasPrefix(entry, "MAKEOVERRIDES=") {
					cmd.Env = append(cmd.Env, entry)
				}
			}
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("dry-run: %v\n%s", err, out)
			}
			var runs []string
			gate := false
			for _, line := range strings.Split(string(out), "\n") {
				line = strings.Join(strings.Fields(line), " ")
				if strings.HasPrefix(line, "go test ") {
					runs = append(runs, line)
				}
				if strings.Contains(line, "go-test-coverage --config=.testcoverage.yml") {
					gate = true
				}
			}
			if len(runs) != 1 || runs[0] != tc.want {
				t.Fatalf("test invocations %q, want exactly %q\n%s", runs, tc.want, out)
			}
			if gate != tc.coverage {
				t.Fatalf("coverage gate=%v, want %v\n%s", gate, tc.coverage, out)
			}
		})
	}
}
