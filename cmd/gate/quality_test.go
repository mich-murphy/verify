package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

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
