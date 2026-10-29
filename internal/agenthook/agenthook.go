// Package agenthook screens agent tool calls for hook bypasses and edits to
// hook or Verify configuration (SPEC §9). It is noise reduction, not
// enforcement: anything it cannot parse is allowed.
package agenthook

import (
	"encoding/json"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const maxInput = 1 << 20

// Verdict is the outcome of screening one tool call.
type Verdict struct {
	Block   bool
	Reason  string // one line; set when Block
	Warning string // set for malformed input (still allowed)
}

// Evaluate reads one tool-call JSON (Pi {toolName,input} or Claude Code
// {tool_name,tool_input}) and screens it.
func Evaluate(r io.Reader) Verdict {
	data, err := io.ReadAll(io.LimitReader(r, maxInput))
	if err != nil {
		return Verdict{Warning: "cannot read tool call: " + err.Error()}
	}
	var ev struct {
		ToolName   string          `json:"toolName"`
		Input      json.RawMessage `json:"input"`
		ToolName2  string          `json:"tool_name"`
		ToolInput2 json.RawMessage `json:"tool_input"`
	}
	if err := json.Unmarshal(data, &ev); err != nil {
		return Verdict{Warning: "malformed tool call JSON: " + err.Error()}
	}
	name, raw := ev.ToolName, ev.Input
	if name == "" {
		name, raw = ev.ToolName2, ev.ToolInput2
	}
	if name == "" {
		return Verdict{Warning: "tool call has no tool name"}
	}
	var in struct {
		Command  string `json:"command"`
		Path     string `json:"path"`
		FilePath string `json:"file_path"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &in); err != nil {
			return Verdict{Warning: "malformed tool input: " + err.Error()}
		}
	}
	reason := ""
	switch strings.ToLower(name) {
	case "bash":
		reason = CheckCommand(in.Command)
	case "write", "edit", "multiedit":
		reason = CheckPath(in.Path + in.FilePath)
	}
	return Verdict{Block: reason != "", Reason: reason}
}

var protectedFiles = map[string]bool{
	"verify.yaml": true, ".pre-commit-config.yaml": true, "prek.toml": true, "lefthook.yml": true,
}

// CheckPath returns a block reason when editing p would change hook or Verify config.
func CheckPath(p string) string {
	parts := strings.Split(path.Clean(filepath.ToSlash(p)), "/")
	if protectedFiles[parts[len(parts)-1]] {
		return "editing " + parts[len(parts)-1] + " changes hook or Verify config; ask the user to change it"
	}
	for i, part := range parts {
		if part == ".husky" || part == ".git" && i+1 < len(parts) && (parts[i+1] == "hooks" || parts[i+1] == "config") {
			return "editing git hooks or .git/config changes hook behaviour; ask the user to change it"
		}
	}
	return ""
}

// CheckCommand returns a block reason when a shell command would bypass hooks.
func CheckCommand(cmd string) string { return checkCommand(cmd, 0) }

func checkCommand(cmd string, depth int) string {
	for _, words := range split(cmd) {
		if r := checkWords(words, depth); r != "" {
			return r
		}
	}
	return ""
}

// split breaks a command line into simple commands of words, honouring
// quotes, backslash escapes, comments and the separators ; & | ( ) ` newline.
// Redirections are not interpreted.
func split(s string) [][]string {
	var (
		cmds [][]string
		cur  []string
		w    strings.Builder
		has  bool
	)
	endWord := func() {
		if has {
			cur = append(cur, w.String())
		}
		w.Reset()
		has = false
	}
	endCmd := func() {
		endWord()
		if len(cur) > 0 {
			cmds = append(cmds, cur)
		}
		cur = nil
	}
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch c := rs[i]; {
		case c == '\\' && i+1 < len(rs):
			i++
			if rs[i] != '\n' {
				w.WriteRune(rs[i])
				has = true
			}
		case c == '\'' || c == '"':
			has = true
			var q string
			q, i = quoted(rs, i)
			w.WriteString(q)
		case c == ' ' || c == '\t' || c == '\r':
			endWord()
		case strings.ContainsRune("\n;&|()`", c):
			endCmd()
		case c == '#' && !has:
			for i < len(rs) && rs[i] != '\n' {
				i++
			}
			endCmd()
		default:
			w.WriteRune(c)
			has = true
		}
	}
	endCmd()
	return cmds
}

// quoted returns the contents of the quoted string opening at rs[i] and the index of its closing quote.
func quoted(rs []rune, i int) (string, int) {
	q, c := strings.Builder{}, rs[i]
	for i++; i < len(rs) && rs[i] != c; i++ {
		if c == '"' && rs[i] == '\\' && i+1 < len(rs) && strings.ContainsRune("\"\\$`", rs[i+1]) {
			i++
		}
		q.WriteRune(rs[i])
	}
	return q.String(), i
}

var wrappers = map[string]bool{
	"env": true, "export": true, "command": true, "exec": true, "sudo": true,
	"nohup": true, "time": true, "builtin": true, "nice": true,
}

func isAssign(w string) bool {
	name, _, ok := strings.Cut(w, "=")
	return ok && name != "" && strings.Trim(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_") == "" &&
		(name[0] < '0' || name[0] > '9')
}

// checkWords skips env assignments and wrappers, then checks git and shell -c.
func checkWords(words []string, depth int) string {
	for len(words) > 0 {
		w := words[0]
		switch {
		case isAssign(w):
			if name, _, _ := strings.Cut(w, "="); name == "SKIP" || name == "PREK_SKIP" {
				return name + "= skips hooks; run the hooks instead"
			}
			words = words[1:]
		case wrappers[path.Base(w)]:
			words = words[1:]
			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				words = words[1:]
			}
		default:
			return checkCommandWords(path.Base(w), words[1:], depth)
		}
	}
	return ""
}

var mutators = map[string]bool{
	"rm": true, "mv": true, "cp": true, "chmod": true, "chown": true, "ln": true, "tee": true,
	"sed": true, "truncate": true, "touch": true, "install": true, "rmdir": true, "dd": true,
	"unlink": true, "mkdir": true, "rsync": true,
}

func checkCommandWords(cmd string, args []string, depth int) string {
	for i, a := range args {
		if !strings.Contains(a, ".git/hooks") {
			continue
		}
		redirected := strings.HasPrefix(a, ">") || i > 0 && strings.HasSuffix(args[i-1], ">")
		if redirected || mutators[cmd] || cmd == "find" && (slices.Contains(args, "-delete") || slices.Contains(args, "-exec")) {
			return "modifying .git/hooks changes git hooks"
		}
	}
	switch cmd {
	case "prek", "pre-commit":
		if slices.Contains(args, "uninstall") {
			return cmd + " uninstall removes git hooks"
		}
	case "git":
		return checkGit(args)
	case "sh", "bash", "zsh", "dash":
		for i, a := range args {
			if depth < 3 && i+1 < len(args) && strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "c") {
				return checkCommand(args[i+1], depth+1)
			}
		}
	}
	return ""
}

const hooksPathReason = "core.hooksPath/hook.* config redirects git hooks"

var noVerifyCmds = map[string]bool{"commit": true, "push": true, "merge": true, "rebase": true, "cherry-pick": true, "am": true}

// longWithValue are long options whose value is the next argument.
var longWithValue = map[string]bool{
	"--message": true, "--file": true, "--author": true, "--date": true, "--template": true,
	"--reuse-message": true, "--reedit-message": true, "--fixup": true, "--squash": true,
}

func checkGit(args []string) string {
	sub := 0
	for ; sub < len(args) && strings.HasPrefix(args[sub], "-"); sub++ {
		if strings.Contains(strings.ToLower(args[sub]), "core.hookspath") {
			return hooksPathReason
		}
		switch args[sub] {
		case "-c", "-C", "--git-dir", "--work-tree", "--namespace", "--exec-path":
			sub++
			if sub < len(args) && strings.Contains(strings.ToLower(args[sub]), "core.hookspath") {
				return hooksPathReason
			}
		}
	}
	if sub >= len(args) {
		return ""
	}
	name, rest := args[sub], args[sub+1:]
	if name == "config" {
		return checkGitConfig(rest)
	}
	if !noVerifyCmds[name] {
		return ""
	}
	return checkNoVerify(name, rest)
}

// checkNoVerify scans the arguments after a git subcommand for hook-skipping flags.
func checkNoVerify(name string, rest []string) string {
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--":
			return ""
		case len(a) >= 7 && strings.HasPrefix("--no-verify", a): //nolint:gocritic // a may be an abbreviation of the flag
			return "--no-verify skips git hooks; fix what the hooks report"
		case longWithValue[a]:
			i++
		case name == "commit" && len(a) > 1 && a[0] == '-' && a[1] != '-':
			n, skip := shortFlags(a[1:])
			if n {
				return "git commit -n skips git hooks; fix what the hooks report"
			}
			if skip {
				i++
			}
		}
	}
	return ""
}

// shortFlags reports whether combined short flags include n before any value-taking
// flag, and whether the next word is consumed as a value (e.g. -m x).
func shortFlags(f string) (n, nextIsValue bool) {
	for j := 0; j < len(f); j++ {
		if f[j] == 'n' {
			return true, false
		}
		if strings.IndexByte("mFCct", f[j]) >= 0 {
			return false, j == len(f)-1
		}
	}
	return false, false
}

// checkGitConfig blocks writes to core.hooksPath or hook.*; reads are allowed.
func checkGitConfig(args []string) string {
	hit := false
	for _, a := range args {
		switch l := strings.ToLower(a); {
		case strings.HasPrefix(l, "--get") || l == "--list" || l == "-l":
			return ""
		case strings.HasPrefix(l, "core.hookspath") || strings.HasPrefix(l, "hook."):
			hit = true
		}
	}
	if hit {
		return hooksPathReason
	}
	return ""
}
