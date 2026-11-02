package onboard

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/mich-murphy/verify/internal/state"
)

// job is one command another hook manager runs.
type job struct{ name, run string }

func normalize(cmd string) string { return strings.Join(strings.Fields(cmd), " ") }

// HookManagers reports lefthook, husky and lint-staged setups in dir whose
// jobs have no counterpart in the prek config text final, matching by command
// text. It never modifies those files.
func HookManagers(dir, final string) []state.Finding {
	var cfg struct {
		Repos []struct {
			Hooks []struct {
				Entry string `yaml:"entry"`
			} `yaml:"hooks"`
		} `yaml:"repos"`
	}
	_ = yaml.Unmarshal([]byte(final), &cfg) // an unparsable final text has no counterparts
	have := map[string]bool{}
	for _, r := range cfg.Repos {
		for _, h := range r.Hooks {
			have[normalize(h.Entry)] = true
		}
	}
	var msgs []string
	for _, src := range managerSources {
		name, jobs, readable := src(dir)
		if name == "" {
			continue
		}
		if !readable {
			msgs = append(msgs, name+" is a JavaScript config; compare its jobs with the prek hooks by hand (the file is left untouched)")
			continue
		}
		var missing []string
		for _, j := range jobs {
			if !have[normalize(j.run)] {
				missing = append(missing, j.name+" ("+j.run+")")
			}
		}
		if len(missing) == 0 {
			msgs = append(msgs, name+": every job has a counterpart in the prek config (the file is left untouched)")
			continue
		}
		msgs = append(msgs, name+" has jobs with no counterpart in the prek config (the file is left untouched): "+strings.Join(missing, ", "))
	}
	if len(msgs) == 0 {
		return nil
	}
	return []state.Finding{{
		ID: "setup:hook-manager", Source: state.SourceSetup, Rule: "setup:hook-manager",
		Severity: state.SeverityNote, State: state.StateOpen, Message: strings.Join(msgs, "; "),
	}}
}

// managerSources return the file or directory name, its jobs, and whether the
// jobs could be read; an empty name means the manager is absent.
var managerSources = []func(dir string) (string, []job, bool){lefthookJobs, huskyJobs, lintStagedJobs}

func readFile(dir, name string) ([]byte, bool) {
	b, err := os.ReadFile(filepath.Join(dir, name)) // #nosec G304 -- fixed names under the repo root
	return b, err == nil
}

func lefthookJobs(dir string) (string, []job, bool) {
	for _, name := range []string{"lefthook.yml", ".lefthook.yml"} {
		b, ok := readFile(dir, name)
		if !ok {
			continue
		}
		var doc yaml.Node
		if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 {
			return name, nil, true
		}
		var jobs []job
		for _, hook := range mapValues(doc.Content[0]) {
			jobs = append(jobs, lefthookHook(hook)...)
		}
		return name, jobs, true
	}
	return "", nil, false
}

// mapValues returns the values of a mapping node in file order.
func mapValues(n *yaml.Node) []*yaml.Node {
	var out []*yaml.Node
	if n.Kind == yaml.MappingNode {
		for i := 1; i < len(n.Content); i += 2 {
			out = append(out, n.Content[i])
		}
	}
	return out
}

func field(n *yaml.Node, key string) *yaml.Node {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
	}
	return nil
}

// lefthookHook reads the `jobs` list (with nested groups) and the `commands`
// mapping of one lefthook hook.
func lefthookHook(hook *yaml.Node) []job {
	var out []job
	if cmds := field(hook, "commands"); cmds != nil {
		for i := 0; i+1 < len(cmds.Content); i += 2 {
			if run := field(cmds.Content[i+1], "run"); run != nil {
				out = append(out, job{cmds.Content[i].Value, run.Value})
			}
		}
	}
	if jobs := field(hook, "jobs"); jobs != nil {
		out = append(out, lefthookJobList(jobs)...)
	}
	return out
}

func lefthookJobList(list *yaml.Node) []job {
	var out []job
	for _, item := range list.Content {
		if run := field(item, "run"); run != nil {
			name := ""
			if n := field(item, "name"); n != nil {
				name = n.Value
			}
			out = append(out, job{name, run.Value})
		}
		if group := field(item, "group"); group != nil {
			if inner := field(group, "jobs"); inner != nil {
				out = append(out, lefthookJobList(inner)...)
			}
		}
	}
	return out
}

func huskyJobs(dir string) (string, []job, bool) {
	ents, err := os.ReadDir(filepath.Join(dir, ".husky"))
	if err != nil {
		return "", nil, false
	}
	var jobs []job
	for _, e := range ents {
		if e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		b, ok := readFile(dir, filepath.Join(".husky", e.Name()))
		if !ok {
			continue
		}
		for line := range strings.SplitSeq(string(b), "\n") {
			if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, ". ") {
				jobs = append(jobs, job{e.Name(), t})
			}
		}
	}
	return ".husky", jobs, true
}

var lintStagedFiles = []string{".lintstagedrc", ".lintstagedrc.json", ".lintstagedrc.yaml", ".lintstagedrc.yml"}

func lintStagedJobs(dir string) (string, []job, bool) {
	for _, name := range lintStagedFiles {
		if b, ok := readFile(dir, name); ok {
			return name, lintStagedMap(b), true
		}
	}
	for _, name := range []string{"lint-staged.config.js", "lint-staged.config.mjs", "lint-staged.config.cjs", ".lintstagedrc.js", ".lintstagedrc.mjs", ".lintstagedrc.cjs"} {
		if _, ok := readFile(dir, name); ok {
			return name, nil, false
		}
	}
	if b, ok := readFile(dir, "package.json"); ok {
		var pkg struct {
			LintStaged json.RawMessage `json:"lint-staged"`
		}
		if json.Unmarshal(b, &pkg) == nil && pkg.LintStaged != nil {
			return "package.json (lint-staged)", lintStagedMap(pkg.LintStaged), true
		}
	}
	return "", nil, false
}

// lintStagedMap reads a glob to command(s) mapping; YAML parses JSON too.
func lintStagedMap(b []byte) []job {
	var doc yaml.Node
	if yaml.Unmarshal(b, &doc) != nil || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil
	}
	m := doc.Content[0]
	var out []job
	for i := 0; i+1 < len(m.Content); i += 2 {
		glob, v := m.Content[i].Value, m.Content[i+1]
		cmds := []*yaml.Node{v}
		if v.Kind == yaml.SequenceNode {
			cmds = v.Content
		}
		for _, c := range cmds {
			out = append(out, job{glob, c.Value})
		}
	}
	return slices.DeleteFunc(out, func(j job) bool { return j.run == "" })
}
