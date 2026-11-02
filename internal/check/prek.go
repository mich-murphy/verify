package check

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mich-murphy/verify/internal/state"
)

// hookHeader matches the `<name>.....Passed` line prek prints per hook. Hook
// output is indented two spaces, so a header is the only unindented shape.
var hookHeader = regexp.MustCompile(`^(\S.*?)\.{3,}(?:\(.*\))?(Passed|Failed|Skipped)$`)

const (
	maxExcerptLines = 20
	maxExcerptBytes = 2048
)

// prekHook is one hook's result and the output lines it printed.
type prekHook struct {
	state.HookResult
	Body []string
}

func hookResults(hooks []prekHook) []state.HookResult {
	var out []state.HookResult
	for _, h := range hooks {
		out = append(out, h.HookResult)
	}
	return out
}

// parsePrekOutput turns `prek run --verbose --color never` output into one entry per
// hook, in order. Prek names hooks by display name in the header; the id comes from
// the `- hook id:` line, which prek omits for skipped hooks, so those keep their
// display name. The second and later hooks sharing an id get `#2`, `#3` appended.
func parsePrekOutput(out []byte) []prekHook {
	p := &prekParser{seen: map[string]int{}}
	for _, line := range strings.Split(string(out), "\n") {
		p.feed(line)
	}
	p.end()
	return p.hooks
}

type prekParser struct {
	hooks    []prekHook
	seen     map[string]int // hooks per id so far
	cur      *state.HookResult
	status   string
	modified bool
	attrs    bool // still reading the `- key: value` lines under a header
	body     []string
}

func (p *prekParser) feed(line string) {
	if m := hookHeader.FindStringSubmatch(line); m != nil {
		p.end()
		p.cur, p.status, p.modified, p.attrs = &state.HookResult{ID: m[1]}, m[2], false, true
		return
	}
	switch {
	case p.cur == nil:
	case p.attrs && strings.HasPrefix(line, "- "):
		p.attribute(strings.TrimPrefix(line, "- "))
	case p.attrs && line == "":
		p.attrs = false
	default:
		p.attrs = false
		p.body = append(p.body, strings.TrimPrefix(line, "  "))
	}
}

func (p *prekParser) attribute(attr string) {
	k, v, _ := strings.Cut(attr, ": ")
	switch k {
	case "hook id":
		p.cur.ID = v
	case "duration":
		if d, err := time.ParseDuration(v); err == nil {
			p.cur.DurationMs = d.Milliseconds()
		}
	case "exit code":
		p.cur.ExitCode, _ = strconv.Atoi(v)
	case "files were modified by this hook":
		p.modified = true
	}
}

func (p *prekParser) end() {
	if p.cur == nil {
		return
	}
	switch {
	case p.status == "Passed":
		p.cur.Status = state.HookPass
	case p.status == "Skipped":
		p.cur.Status = state.HookSkipped
	case p.modified:
		p.cur.Status = state.HookModified
	default:
		p.cur.Status = state.HookFail
	}
	for len(p.body) > 0 && p.body[len(p.body)-1] == "" {
		p.body = p.body[:len(p.body)-1]
	}
	p.seen[p.cur.ID]++
	if n := p.seen[p.cur.ID]; n > 1 {
		p.cur.ID += "#" + strconv.Itoa(n)
	}
	p.hooks = append(p.hooks, prekHook{HookResult: *p.cur, Body: p.body})
	p.cur, p.body = nil, nil
}

// stageFindings is the agent-facing result of one prek stage: one finding per
// failed or modifying hook, or one stage finding when prek failed with no
// parseable hook. paths are the tracked files the stage rewrote.
func stageFindings(stage string, sr stepRun, hooks []prekHook, paths []string) []state.Finding {
	var fs []state.Finding
	for _, h := range hooks {
		if h.Status != state.HookFail && h.Status != state.HookModified {
			continue
		}
		id := "check:" + stage + ":" + h.ID
		msg := fmt.Sprintf("%s failed (exit %d)", h.ID, h.ExitCode)
		if first := firstLine(h.Body); first != "" {
			msg += ": " + first
		}
		if h.Status == state.HookModified {
			msg = modifiedMessage(h.ID, paths)
		}
		fs = append(fs, state.Finding{
			ID: id, Source: state.SourceCheck, Rule: id, Severity: state.SeverityBlock,
			Message: msg, Hook: h.ID, Log: sr.step.Log, Excerpt: excerpt(h.Body, false),
		})
	}
	if len(fs) == 0 && sr.step.Status != state.StatusPass {
		fs = append(fs, stepFailure("prek-"+stage, sr))
	}
	return fs
}

func modifiedMessage(hook string, paths []string) string {
	if len(paths) == 0 {
		return hook + " modified files; run the fixer and commit"
	}
	return "modified " + strings.Join(paths, ", ") + "; run the fixer and commit"
}

func firstLine(lines []string) string {
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// excerpt keeps at most 20 lines and 2 KiB, from the start of lines or, when
// tail is set, from the end (where installers and crashes print their error).
func excerpt(lines []string, tail bool) string {
	if len(lines) > maxExcerptLines {
		if tail {
			lines = lines[len(lines)-maxExcerptLines:]
		} else {
			lines = lines[:maxExcerptLines]
		}
	}
	s := strings.Join(lines, "\n")
	if len(s) <= maxExcerptBytes {
		return s
	}
	if tail {
		return strings.ToValidUTF8(s[len(s)-maxExcerptBytes:], "")
	}
	return strings.ToValidUTF8(s[:maxExcerptBytes], "")
}

// budgetNote names the three slowest hooks when a stage exceeds its SPEC §4.5 budget.
func budgetNote(stage string, d, budget time.Duration, hooks []state.HookResult) []state.Finding {
	if d <= budget {
		return nil
	}
	slow := append([]state.HookResult(nil), hooks...)
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].DurationMs > slow[j].DurationMs })
	var parts []string
	for _, h := range slow[:min(3, len(slow))] {
		parts = append(parts, fmt.Sprintf("%s %.1fs", h.ID, float64(h.DurationMs)/1000))
	}
	msg := fmt.Sprintf("%s took %.1fs, over the %.0fs budget (SPEC §4.5)", stage, d.Seconds(), budget.Seconds())
	if len(parts) > 0 {
		msg += "; slowest hooks: " + strings.Join(parts, ", ")
	}
	id := "check:budget:" + stage
	return []state.Finding{{ID: id, Source: state.SourceCheck, Rule: id, Severity: state.SeverityNote, Message: msg}}
}
