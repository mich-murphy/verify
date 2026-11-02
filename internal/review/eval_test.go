package review

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mich-murphy/verify/internal/state"
)

var (
	reviewEval       = flag.String("review-eval", "", "opt-in subscription reviewer: codex or claude")
	reviewEvalOutput = flag.String("review-eval-output", "", "new JSON output file (required for live evaluation)")
	reviewEvalCase   = flag.String("review-eval-case", "", "one case ID, or every case when empty")
)

func openIDs(in Input) []string {
	ids := make([]string, 0, len(in.Open))
	for _, f := range in.Open {
		ids = append(ids, f.ID)
	}
	return ids
}

// scoreResponse compares a validated reviewer answer with the case's Expect.
// Failures fail the case. Notes record extra findings on a bug case, so
// precision is visible without failing recall.
func scoreResponse(e Expect, r *Response) (failures, notes []string) {
	describe := func(f state.Finding) string {
		return fmt.Sprintf("%s %s:%d severity=%s repro=%t", f.Rule, f.Path, f.Line, f.Severity, f.Repro != nil)
	}
	if e.silent() {
		for _, f := range r.Findings {
			failures = append(failures, "unexpected finding: "+describe(f))
		}
	} else {
		hit := slices.IndexFunc(r.Findings, func(f state.Finding) bool {
			return f.Path == e.Path && f.Rule == e.Kind &&
				(f.Severity == state.SeverityBlock) == e.Blocking && (!e.Repro || f.Repro != nil)
		})
		if hit < 0 {
			got := make([]string, 0, len(r.Findings))
			for _, f := range r.Findings {
				got = append(got, describe(f))
			}
			failures = append(failures, fmt.Sprintf("no finding with path=%s kind=%s blocking=%t repro=%t; got [%s]",
				e.Path, e.Kind, e.Blocking, e.Repro, strings.Join(got, "; ")))
		}
		for i, f := range r.Findings {
			if hit >= 0 && i != hit {
				notes = append(notes, "extra finding: "+describe(f))
			}
		}
	}
	if !slices.Equal(slices.Sorted(slices.Values(r.Resolved)), slices.Sorted(slices.Values(e.Resolved))) {
		failures = append(failures, fmt.Sprintf("resolved %v, want %v", r.Resolved, e.Resolved))
	}
	return failures, notes
}

type evalTokens struct{ Input, Output, CacheRead, CacheCreation int }

// wrapperInfo reads the model names and token counts out of a CLI wrapper:
// claude's single result object or codex's JSONL events.
func wrapperInfo(wrapper []byte) (models []string, tokens evalTokens) {
	for line := range bytes.SplitSeq(wrapper, []byte("\n")) {
		var ev struct {
			Model      string                     `json:"model"`
			ModelUsage map[string]json.RawMessage `json:"modelUsage"`
			Usage      map[string]int             `json:"usage"`
		}
		if json.Unmarshal(line, &ev) != nil {
			continue
		}
		if ev.Model != "" {
			models = append(models, ev.Model)
		}
		for m := range ev.ModelUsage {
			models = append(models, m)
		}
		tokens.Input += ev.Usage["input_tokens"]
		tokens.Output += ev.Usage["output_tokens"]
		tokens.CacheRead += ev.Usage["cache_read_input_tokens"] + ev.Usage["cached_input_tokens"]
		tokens.CacheCreation += ev.Usage["cache_creation_input_tokens"]
	}
	slices.Sort(models)
	return slices.Compact(models), tokens
}

type evalObservation struct {
	Case, Reviewer, RequestedModel, PromptSHA256, PacketSHA256, Duration string
	ReportedModels                                                       []string
	Tokens                                                               evalTokens
	Wrapper, Raw                                                         string
	Parsed                                                               *Response
	Pass                                                                 bool
	Failures, Notes                                                      []string
	Error                                                                string `json:",omitempty"`
}

type evalCase struct {
	c      corpusCase
	packet []byte
}

// recorder holds the wrapper of the latest reviewer call.
type recorder struct{ wrapper []byte }

func (r *recorder) capture(wrapper []byte) { r.wrapper = wrapper }

// evaluate makes one call per case. It stops at the first ErrUnavailable and
// returns it with the observations so far, so an outage costs one attempt.
func evaluate(ctx context.Context, rv Reviewer, rec *recorder, model string, cases []evalCase, persist func([]evalObservation)) ([]evalObservation, error) {
	var results []evalObservation
	for _, p := range cases {
		callCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		rec.wrapper = nil
		start := time.Now()
		raw, _, runErr := rv.Run(callCtx, p.packet)
		cancel()
		packetSum := sha256.Sum256(p.packet)
		promptSum := sha256.Sum256(append([]byte(SystemPrompt+"\n\n=== PACKET ===\n"), p.packet...))
		obs := evalObservation{
			Case: p.c.ID, Reviewer: rv.Name(), RequestedModel: model,
			PromptSHA256: hex.EncodeToString(promptSum[:]), PacketSHA256: hex.EncodeToString(packetSum[:]),
			Wrapper: string(rec.wrapper), Raw: string(raw), Duration: time.Since(start).String(),
		}
		obs.ReportedModels, obs.Tokens = wrapperInfo(rec.wrapper)
		if runErr != nil {
			obs.Error = runErr.Error()
		} else if parsed, parseErr := Parse(raw, openIDs(p.c.Input), []string{p.c.Path}); parseErr != nil {
			obs.Error = fmt.Sprintf("response validation: %v", parseErr)
		} else {
			obs.Parsed = parsed
			obs.Failures, obs.Notes = scoreResponse(p.c.Expect, parsed)
		}
		obs.Pass = obs.Error == "" && len(obs.Failures) == 0
		results = append(results, obs)
		persist(results)
		if errors.Is(runErr, ErrUnavailable) {
			return results, runErr
		}
	}
	return results, nil
}

// TestReviewLiveEvaluation does not invoke any reviewer without explicit flags.
func TestReviewLiveEvaluation(t *testing.T) {
	if *reviewEval == "" {
		t.Log("live review disabled; supply -review-eval=codex|claude and -review-eval-output=NEW_FILE")
		return
	}
	if *reviewEvalOutput == "" || (*reviewEval != "codex" && *reviewEval != "claude") {
		t.Fatal("live review requires -review-eval=codex|claude and -review-eval-output=NEW_FILE")
	}
	claudeModel := cmp.Or(os.Getenv("VERIFY_CLAUDE_MODEL"), "opus")
	codexModel := os.Getenv("VERIFY_CODEX_MODEL")
	reviewer, err := Select(*reviewEval, "", claudeModel, codexModel)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	model := cmp.Or(codexModel, "CLI default")
	switch rv := reviewer.(type) {
	case *Claude:
		rv.Capture, model = rec.capture, claudeModel
	case *Codex:
		rv.Capture = rec.capture
	}
	var cases []evalCase
	for _, c := range validateCorpus(t) {
		if *reviewEvalCase != "" && c.ID != *reviewEvalCase {
			continue
		}
		packet, buildErr := Build(c.Input)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		cases = append(cases, evalCase{c, packet})
	}
	if len(cases) == 0 {
		t.Fatalf("unknown case %q", *reviewEvalCase)
	}
	// Reserve and test the destination BEFORE any charged calls.
	out, err := os.OpenFile(*reviewEvalOutput, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	persist := func(results []evalObservation) {
		t.Helper()
		encoded, encodeErr := json.MarshalIndent(results, "", "  ")
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if err := out.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := out.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(append(encoded, '\n')); err != nil {
			t.Fatal(err)
		}
		if err := out.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	persist(nil)
	results, err := evaluate(t.Context(), reviewer, rec, model, cases, persist)
	passed := 0
	for _, o := range results {
		if o.Pass {
			passed++
			continue
		}
		t.Errorf("%s: error=%q failures=%v", o.Case, o.Error, o.Failures)
	}
	t.Logf("%d/%d cases passed; per-call detail in %s", passed, len(cases), *reviewEvalOutput)
	if err != nil {
		t.Fatalf("aborted after %d of %d cases: %v", len(results), len(cases), err)
	}
}
