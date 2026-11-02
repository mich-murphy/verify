package audit

import (
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Note is an advisory about a hook that satisfies an obligation but would
// misbehave; it never blocks.
type Note struct{ ID, Message string }

// scoped lists the obligations whose hook must reach the project's main language.
var scoped = []string{"format", "lint", "typecheck", "test", "coverage"}

// sample is a representative file of a language with its identify tags.
type sample struct {
	path string
	tags []string
}

func samples(goP, tsP bool, tsDir string) []sample {
	var out []sample
	if goP {
		out = append(out,
			sample{"main.go", []string{"file", "text", "go"}},
			sample{"internal/x/x_test.go", []string{"file", "text", "go"}},
		)
	}
	if tsP {
		out = append(out,
			sample{path.Join(tsDir, "src/index.ts"), []string{"file", "text", "ts"}},
			sample{path.Join(tsDir, "src/App.tsx"), []string{"file", "text", "tsx"}},
		)
	}
	return out
}

// reaches reports whether the hook's files, exclude and types filters admit
// the sample. An unparsable regex admits it: prek's dialect may differ.
func (h hook) reaches(s sample) bool {
	if h.Files != "" {
		if re, err := regexp.Compile(h.Files); err == nil && !re.MatchString(s.path) {
			return false
		}
	}
	if h.Exclude != "" {
		if re, err := regexp.Compile(h.Exclude); err == nil && re.MatchString(s.path) {
			return false
		}
	}
	for _, t := range h.Types {
		if !slices.Contains(s.tags, t) {
			return false
		}
	}
	return len(h.TypesOr) == 0 || slices.ContainsFunc(h.TypesOr, func(t string) bool { return slices.Contains(s.tags, t) })
}

func (a *auditor) note(hookID, reason, msg string) {
	id := "setup:" + hookID + ":" + reason
	if !slices.ContainsFunc(a.notes, func(n Note) bool { return n.ID == id }) {
		a.notes = append(a.notes, Note{ID: id, Message: msg})
	}
}

// ruleNotes records the scope, always-run and uncacheable notes for the hooks
// that meet obligation r.
func (a *auditor) ruleNotes(r rule, lvl string, ms []hook, goP, tsP bool) {
	smp := samples(goP, tsP, a.tsDir)
	var langs []string
	if goP {
		langs = append(langs, "go")
	}
	if tsP {
		langs = append(langs, "typescript")
	}
	for _, h := range ms {
		if lvl == Required && slices.Contains(scoped, r.id) && !slices.ContainsFunc(smp, h.reaches) {
			a.note(h.ID, "scope", fmt.Sprintf("hook %s: its files/types filters cannot match any %s file, so it never checks this project", h.ID, strings.Join(langs, " or ")))
		}
		if r.full && h.at(push) && h.AlwaysRun {
			a.note(h.ID, "always-run", fmt.Sprintf("hook %s is always_run at pre-push: docs-only commits will pay for it", h.ID))
		}
		if goP && lvl == Required && (r.id == "test" || r.id == "coverage") && Uncached(a.command(h.Entry)) {
			a.note(h.ID, "uncacheable", fmt.Sprintf("hook %s: `%s` runs Go tests uncached (-count=1 or -shuffle), so prek cannot reuse results", h.ID, h.Entry))
		}
	}
}

// command expands `make <target>` to the Makefile recipe; any other entry is
// returned as written.
func (a *auditor) command(entry string) string {
	f := strings.Fields(entry)
	if len(f) < 2 || f[0] != "make" {
		return entry
	}
	for _, name := range []string{"Makefile", "makefile", "GNUmakefile"} {
		if text, ok := a.read(name); ok {
			if recipe := MakeRecipe(text, f[len(f)-1]); recipe != "" {
				return recipe
			}
			break
		}
	}
	return entry
}

var shellBuiltins = []string{".", ":", "[", "cd", "echo", "eval", "exec", "exit", "export", "false", "set", "source", "test", "true"}

var envAssign = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// toolNotes records tool-missing for system hooks whose command is neither
// make, a shell builtin, nor found.
func (a *auditor) toolNotes(hooks []hook) {
	for _, h := range hooks {
		if !h.runs() || h.Language != "system" {
			continue
		}
		words := slices.DeleteFunc(strings.Fields(h.Entry), func(w string) bool { return envAssign.MatchString(w) })
		if len(words) == 0 || words[0] == "make" || slices.Contains(shellBuiltins, words[0]) || a.found(words[0]) {
			continue
		}
		a.note(h.ID, "tool-missing", fmt.Sprintf("hook %s: %s is not on PATH", h.ID, words[0]))
	}
}

func (a *auditor) found(tool string) bool {
	if strings.Contains(tool, "/") {
		return fileExists(filepath.Join(a.dir, tool)) || filepath.IsAbs(tool) && fileExists(tool)
	}
	_, err := exec.LookPath(tool)
	return err == nil
}
