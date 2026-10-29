// Package audit implements the setup audit (SPEC §4.3). Hooks come from
// `prek list`; every other obligation is read from tool config files. The
// audit never reads Makefiles or package scripts.
package audit

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/pkgmgr"
	"github.com/mich-murphy/verify/internal/state"
)

// Obligation levels.
const (
	Required    = "required"
	Recommended = "recommended"
)

// Obligation is one expectation of the quality-setup profile.
type Obligation struct {
	ID        string
	Level     string // required | recommended
	Satisfied bool
	Waived    bool
	Hook      string // the prek hook that met a hook obligation
	Detail    string // why it is unmet
	Fix       string // concrete config snippet
}

// Report is the audit outcome for a directory.
type Report struct {
	Profile     string // go | typescript | mixed | ""
	Obligations []Obligation
}

// Status words used when rendering an obligation.
const (
	StatusOK          = "ok"
	StatusMissing     = "missing"
	StatusWaived      = "waived"
	StatusRecommended = "recommended"
)

// Status classifies an obligation for display.
func (o Obligation) Status() string {
	switch {
	case o.Satisfied:
		return StatusOK
	case o.Waived:
		return StatusWaived
	case o.Level == Required:
		return StatusMissing
	}
	return StatusRecommended
}

// Blocking reports whether any required obligation is unmet and unwaived.
func (r Report) Blocking() bool {
	return slices.ContainsFunc(r.Obligations, func(o Obligation) bool {
		return o.Level == Required && !o.Satisfied && !o.Waived
	})
}

// Findings converts unmet, unwaived obligations into findings.
func (r Report) Findings() []state.Finding {
	var out []state.Finding
	for _, o := range r.Obligations {
		if o.Satisfied || o.Waived {
			continue
		}
		sev := state.SeverityNote
		if o.Level == Required {
			sev = state.SeverityBlock
		}
		msg := o.Detail
		if o.Fix != "" {
			msg += " Fix: " + o.Fix
		}
		out = append(out, state.Finding{
			ID: "setup:" + o.ID, Source: state.SourceSetup, Rule: "setup:" + o.ID,
			Severity: sev, Message: msg, State: state.StateOpen,
		})
	}
	return out
}

type auditor struct {
	ctx context.Context
	dir string
	cfg config.Config
	err error
	obs []Obligation
}

// Run audits dir.
func Run(ctx context.Context, dir string, cfg config.Config) (Report, error) {
	a := &auditor{ctx: ctx, dir: dir, cfg: cfg}
	goP := a.exists("go.mod")
	ts, tsP := tsProject(dir)
	rep := Report{}
	switch {
	case goP && tsP:
		rep.Profile = "mixed"
	case goP:
		rep.Profile = "go"
	case tsP:
		rep.Profile = "typescript"
	default:
		rep.Profile = "unsupported"
	}
	a.hookObligations(goP, tsP)
	if goP {
		a.goFiles()
	}
	if tsP {
		a.tsFiles(ts)
	}
	a.add("ci", Recommended, a.noWorkflows(), "add a .github/workflows job that runs the same checks as the hooks (for example `prek run --all-files`)")
	rep.Obligations = a.obs
	return rep, a.err
}

// add records an obligation that is met when problem is empty.
func (a *auditor) add(id, level, problem, fix string) {
	o := Obligation{ID: id, Level: level, Waived: a.cfg.Waived(id), Satisfied: problem == ""}
	if !o.Satisfied {
		o.Detail, o.Fix = problem, fix
	}
	a.obs = append(a.obs, o)
}

func (a *auditor) setErr(err error) {
	if a.err == nil {
		a.err = err
	}
}

// read returns a file under dir; a failure other than "missing" fails the
// whole audit closed.
func (a *auditor) read(rel string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(a.dir, filepath.FromSlash(rel))) // #nosec G304 -- the audit reads repository files by design
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			a.setErr(fmt.Errorf("read %s: %w", rel, err))
		}
		return "", false
	}
	return string(b), true
}

func (a *auditor) exists(rel string) bool {
	return fileExists(filepath.Join(a.dir, filepath.FromSlash(rel)))
}

func fileExists(p string) bool { _, err := os.Stat(p); return err == nil }

// workflows lists the CI workflow files.
func (a *auditor) workflows() []string {
	ents, err := os.ReadDir(filepath.Join(a.dir, ".github", "workflows"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.setErr(err)
	}
	var out []string
	for _, e := range ents {
		if ext := filepath.Ext(e.Name()); !e.IsDir() && (ext == ".yml" || ext == ".yaml") {
			out = append(out, e.Name())
		}
	}
	return out
}

func (a *auditor) noWorkflows() string {
	if len(a.workflows()) == 0 {
		return "no CI workflow found"
	}
	return ""
}

// tsProject returns the TypeScript project directory (relative to dir): the
// root, or the single nested project when the root has no lockfile.
func tsProject(dir string) (string, bool) {
	sub, _ := pkgmgr.Project(dir)
	d := path.Join(".", sub)
	return d, fileExists(filepath.Join(dir, d, "package.json"))
}

// readHooks records discovery failures separately from unmet hook obligations.
func (a *auditor) readHooks() ([]hook, bool) {
	hooks, found, err := listHooks(a.ctx, a.dir)
	if errors.Is(err, ErrUnsettled) {
		a.setErr(err)
	}
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	a.add("prek-config", Required, detail, "run `verify setup --apply`, or create .pre-commit-config.yaml with `repo: local` hooks whose ids are the standard obligation ids (SPEC §4.3)")
	return hooks, !found || err == nil
}

func (a *auditor) hookObligations(goP, tsP bool) {
	hooks, known := a.readHooks()
	if !known {
		return // discovery failed; do not infer which hooks are missing
	}
	var early []string
	for _, r := range rules {
		lvl := r.level(goP, tsP)
		if lvl == "" || r.id == "actionlint" && len(a.workflows()) == 0 {
			continue
		}
		ms := matching(hooks, r.names())
		ids := make([]string, len(ms))
		for i, h := range ms {
			ids[i] = h.ID
			if r.full && h.at("pre-commit") {
				early = append(early, h.ID)
			}
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		o := Obligation{ID: r.id, Level: lvl, Waived: a.cfg.Waived(r.id), Satisfied: len(ms) > 0, Hook: strings.Join(ids, ", ")}
		if !o.Satisfied {
			o.Detail = fmt.Sprintf("no prek hook for %s at pre-commit or pre-push (accepted ids: %s)", r.id, strings.Join(r.names(), ", "))
			o.Fix = fmt.Sprintf("add a %s hook with id %q to the prek config (`verify setup --apply` writes it)", r.stage, r.id)
		}
		a.obs = append(a.obs, o)
	}
	slices.Sort(early)
	early = slices.Compact(early)
	a.add("hook-tiers", Recommended, joinIf(len(early) > 0, "full-tier hooks run at pre-commit: ", early),
		"move type checking, tests, coverage, dead code and vulnerability scans to stages: [pre-push] (SPEC §4.5)")
	var missing []string
	for _, id := range []string{"trailing-whitespace", "end-of-file-fixer", "check-merge-conflict"} {
		if !slices.ContainsFunc(hooks, func(h hook) bool { return h.ID == id && h.runs() }) {
			missing = append(missing, id)
		}
	}
	a.add("hygiene", Recommended, joinIf(len(missing) > 0, "no hygiene hooks: ", missing), "add prek `repo: builtin` hooks trailing-whitespace, end-of-file-fixer and check-merge-conflict")
}

func joinIf(cond bool, prefix string, ids []string) string {
	if !cond {
		return ""
	}
	return prefix + strings.Join(ids, ", ")
}
