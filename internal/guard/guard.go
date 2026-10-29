// Package guard implements the Gate-owned in-process guards (SPEC §4.1:
// secrets, suppressions, tests-removed, generated, config).
//
// Contract: Run inspects the diff from Input.Base to Input.SHA (read through
// Input.Repo; Input.Worktree is a detached checkout of SHA when non-empty) and
// returns findings with Source "guard", Rule "guard:<name>" and the
// deterministic ID "guard:<name>:<path>[:<line>]". It must not modify the
// repository, must not execute repository code, and returns an error only when
// it could not examine the diff (which fails the check closed).
package guard

import (
	"context"
	"fmt"
	"strings"

	"github.com/mich-murphy/verify/internal/config"
	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/lane"
	"github.com/mich-murphy/verify/internal/state"
)

// Input describes the commit range to examine.
type Input struct {
	Repo     *gitx.Repo
	Base     string // merge-base commit
	SHA      string // candidate commit
	Worktree string // detached worktree of SHA, may be empty
	Config   config.Config
	Fast     bool
	Diff     *gitx.LineDiff
}

// Guard names.
const (
	Secrets      = "secrets"
	Suppressions = "suppressions"
	TestsRemoved = "tests-removed"
	Generated    = "generated"
	Config       = "config"
)

func finding(name, path string, line int, sev, msg string) state.Finding {
	id := "guard:" + name + ":" + path
	if line > 0 {
		id += fmt.Sprintf(":%d", line)
	}
	return state.Finding{
		ID: id, Source: state.SourceGuard, Rule: "guard:" + name, Severity: sev,
		Path: path, Line: line, Message: msg, State: state.StateOpen,
	}
}

// Run executes every guard.
func Run(ctx context.Context, in Input) ([]state.Finding, error) {
	var diff *gitx.LineDiff
	var err error
	if in.Fast {
		diff, err = in.Repo.DiffWorkingTree(ctx)
	} else {
		diff, err = in.Repo.DiffLines(ctx, in.Base, in.SHA)
	}
	if err != nil {
		return nil, fmt.Errorf("diff: %w", err)
	}
	in.Diff = diff
	sec, err := scanSecrets(ctx, in)
	if err != nil {
		return nil, err
	}
	gen, err := generated(ctx, in, diff)
	if err != nil {
		return nil, err
	}
	var out []state.Finding
	out = append(out, sec...)
	out = append(out, suppressions(diff)...)
	out = append(out, testsRemoved(diff)...)
	out = append(out, gen...)
	out = append(out, configNotes(diff)...)
	return out, nil
}

func generated(ctx context.Context, in Input, d *gitx.LineDiff) ([]state.Finding, error) {
	var out []state.Finding
	for _, p := range d.Modified {
		data, found, err := in.Repo.FileAt(ctx, in.Base, p)
		if err != nil {
			return nil, fmt.Errorf("read %s at base: %w", p, err)
		}
		if found && isGeneratedHeader(data) {
			out = append(out, finding(Generated, p, 0, state.SeverityBlock,
				"edits a generated file; change the generator or its input instead"))
		}
	}
	return out, nil
}

func isGeneratedHeader(data []byte) bool {
	lines := strings.SplitN(string(data), "\n", 11)
	lines = lines[:min(10, len(lines))]
	head := strings.Join(lines, "\n")
	return (strings.Contains(head, "Code generated") && strings.Contains(head, "DO NOT EDIT")) ||
		strings.Contains(head, "@generated")
}

func configNotes(d *gitx.LineDiff) []state.Finding {
	var out []state.Finding
	for _, group := range [][]string{d.NewFiles, d.Modified, d.DeletedFiles} {
		for _, p := range group {
			if lane.IsQualityConfig(p) {
				out = append(out, finding(Config, p, 0, state.SeverityNote,
					"quality config changed; the change needs sign-off"))
			}
		}
	}
	return out
}
