package cli

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/state"
)

func init() { register("doctor", runDoctor) }

type tool struct {
	name     string
	required bool
	any      []string // alternative executables; the first found is reported
}

var doctorTools = []tool{
	{name: "git", required: true, any: []string{"git"}},
	{name: "prek", required: true, any: []string{"prek"}},
	{name: "gh", required: true, any: []string{"gh"}},
	{name: "claude", any: []string{"claude"}},
	{name: "codex", any: []string{"codex"}},
}

// firstLine runs `<path> --version` and returns the first output line.
func firstLine(ctx context.Context, path string) string {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, path, "--version").Output() // #nosec G204 -- path comes from LookPath of a fixed tool name.
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line)
}

// authStatus checks the installed CLI's noninteractive authentication state.
// It never reads credential files or exposes the command's output (which may contain secrets).
// An authenticated subscription CLI does not prove entitlement, quota or billing mode.
func authStatus(ctx context.Context, path string, args ...string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, path, args...) // #nosec G204 -- path comes from LookPath of a fixed tool name.
	c.Stdout, c.Stderr = io.Discard, io.Discard
	return c.Run() == nil
}

//nolint:gocognit // linear tool probe loop
func runDoctor(env *Env) int {
	fs := env.Flags("doctor")
	if !env.Parse(fs) || fs.NArg() > 0 {
		return env.Fail("doctor", ExitUsage, "usage: gate doctor [--json]", "fix the arguments")
	}
	var steps []state.Step
	var lines, install, authenticate []string
	reviewerReady := false
	for _, t := range doctorTools {
		path, found := "", ""
		for _, n := range t.any {
			if p, err := exec.LookPath(n); err == nil {
				path, found = p, n
				break
			}
		}
		st := state.Step{Name: t.name, ExitCode: 0, Status: state.StatusPass}
		if path == "" {
			st.Status, st.ExitCode = state.StatusSkipped, 1
			lines = append(lines, fmt.Sprintf("%s: missing", t.name))
			if t.required {
				install = append(install, t.name)
			}
			steps = append(steps, st)
			continue
		}
		line := fmt.Sprintf("%s: present (%s)", t.name, found)
		if v := firstLine(env.Ctx, path); v != "" {
			line += " " + v
		}
		var args []string
		switch t.name {
		case "gh", "claude":
			args = []string{"auth", "status"}
		case "codex":
			args = []string{"login", "status"}
		}
		if args != nil {
			if authStatus(env.Ctx, path, args...) {
				line += "; authenticated"
				if t.name == "claude" || t.name == "codex" {
					reviewerReady = true
				}
			} else {
				line += "; not authenticated"
				st.Status, st.ExitCode = state.StatusFail, 1
				if t.name == "gh" {
					install = append(install, "authenticate gh")
				} else {
					authenticate = append(authenticate, t.name)
				}
			}
		}
		lines = append(lines, line)
		steps = append(steps, st)
	}
	lines = append(lines, "note: authentication status does not establish subscription entitlement, quota or billing mode")
	if !reviewerReady {
		if len(authenticate) > 0 {
			install = append(install, "authenticate a reviewer CLI (claude or codex)")
		} else {
			install = append(install, "a reviewer CLI (claude or codex)")
		}
	}
	res := Result{Command: "doctor", Status: "ok", Steps: steps, Message: strings.Join(lines, "\n")}
	if len(install) > 0 {
		res.Status = "blocked"
		res.NextAction = "install or authenticate " + strings.Join(install, ", ")
		return env.Emit(res, ExitBlocked)
	}
	return env.Emit(res, ExitOK)
}
