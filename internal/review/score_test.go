package review

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestScoreResponse(t *testing.T) {
	bug := Expect{Path: "fixture.go", Kind: "correctness", Blocking: true, Repro: true, Resolved: []string{}}
	silent := Expect{Findings: "none", Resolved: []string{}}
	repair := Expect{Findings: "none", Resolved: []string{"F1"}}
	finding := func(kind, path string, repro string) string {
		return `{"kind":"` + kind + `","path":"` + path + `","line":4,"claim":"c","repro":` + repro + `}`
	}
	rep := `{"input":"x","expected":"y"}`
	answer := func(resolved string, findings ...string) string {
		return `{"findings":[` + strings.Join(findings, ",") + `],"resolved":[` + resolved + `]}`
	}
	tests := []struct {
		name     string
		e        Expect
		raw      string
		failures []string
		notes    []string
	}{
		{"hit", bug, answer("", finding("correctness", "fixture.go", rep)), nil, nil},
		{
			"miss", bug, answer(""),
			[]string{"no finding with path=fixture.go kind=correctness blocking=true repro=true; got []"},
			nil,
		},
		{
			"wrong path", bug, answer("", finding("correctness", "other.go", rep)),
			[]string{"no finding with path=fixture.go kind=correctness blocking=true repro=true; got [correctness other.go:4 severity=block repro=true]"},
			nil,
		},
		{
			"missing repro", bug, answer("", finding("correctness", "fixture.go", "null")),
			[]string{"no finding with path=fixture.go kind=correctness blocking=true repro=true; got [correctness fixture.go:4 severity=note repro=false]"},
			nil,
		},
		{
			"wrong kind", bug, answer("", finding("contract", "fixture.go", rep)),
			[]string{"no finding with path=fixture.go kind=correctness blocking=true repro=true; got [contract fixture.go:4 severity=note repro=true]"},
			nil,
		},
		{
			"extra finding is a note", bug, answer("", finding("correctness", "fixture.go", rep), finding("simplicity", "fixture.go", "null")),
			nil,
			[]string{"extra finding: simplicity fixture.go:4 severity=note repro=false"},
		},
		{
			"silent with finding", silent, answer("", finding("simplicity", "fixture.go", "null")),
			[]string{"unexpected finding: simplicity fixture.go:4 severity=note repro=false"},
			nil,
		},
		{"silent and quiet", silent, answer(""), nil, nil},
		{"resolved exact", repair, answer(`"F1"`), nil, nil},
		{"resolved missing", repair, answer(""), []string{"resolved [], want [F1]"}, nil},
		{
			"resolved extra",
			Expect{Findings: "none", Resolved: []string{"F1"}},
			answer(`"F1","F2"`),
			[]string{"resolved [F1 F2], want [F1]"},
			nil,
		},
		{"resolved when none may be", silent, answer(`"F1"`), []string{"resolved [F1], want []"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := Parse([]byte(tc.raw), []string{"F1", "F2"}, []string{"fixture.go", "other.go"})
			if err != nil {
				t.Fatal(err)
			}
			failures, notes := scoreResponse(tc.e, resp)
			if !slices.Equal(failures, tc.failures) || !slices.Equal(notes, tc.notes) {
				t.Fatalf("failures %q notes %q, want %q and %q", failures, notes, tc.failures, tc.notes)
			}
		})
	}
}

func TestExpectValidate(t *testing.T) {
	for name, e := range map[string]Expect{
		"resolved missing":         {Findings: "none"},
		"silent with a path":       {Findings: "none", Path: "fixture.go", Resolved: []string{}},
		"unknown findings value":   {Findings: "some", Resolved: []string{}},
		"required without path":    {Kind: "correctness", Resolved: []string{}},
		"unknown kind":             {Path: "fixture.go", Kind: "style", Resolved: []string{}},
		"blocking without repro":   {Path: "fixture.go", Kind: "correctness", Blocking: true, Resolved: []string{}},
		"blocking non-correctness": {Path: "fixture.go", Kind: "data", Blocking: true, Repro: true, Resolved: []string{}},
	} {
		if e.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, e := range map[string]Expect{
		"bug":    {Path: "fixture.go", Kind: "correctness", Blocking: true, Repro: true, Resolved: []string{}},
		"note":   {Path: "fixture.go", Kind: "data", Resolved: []string{}},
		"silent": {Findings: "none", Resolved: []string{"F1"}},
	} {
		if err := e.validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

type scriptedReviewer struct {
	answers []error
	calls   int
}

func (s *scriptedReviewer) Name() string { return "scripted" }

func (s *scriptedReviewer) Run(context.Context, []byte) ([]byte, Usage, error) {
	err := s.answers[s.calls]
	s.calls++
	return []byte(`{"findings":[],"resolved":[]}`), Usage{}, err
}

func TestEvaluateStopsAtFirstUnavailable(t *testing.T) {
	quiet := Expect{Findings: "none", Resolved: []string{}}
	cases := make([]evalCase, 3)
	for i := range cases {
		cases[i] = evalCase{c: corpusCase{ID: "c" + string(rune('0'+i)), Path: "fixture.go", Expect: quiet}}
	}
	rv := &scriptedReviewer{answers: []error{nil, ErrUnavailable, nil}}
	var persisted int
	results, err := evaluate(t.Context(), rv, &recorder{}, "m", cases, func(r []evalObservation) { persisted = len(r) })
	if !errors.Is(err, ErrUnavailable) || rv.calls != 2 || len(results) != 2 || persisted != 2 {
		t.Fatalf("err=%v calls=%d results=%d persisted=%d", err, rv.calls, len(results), persisted)
	}
	if !results[0].Pass || results[1].Pass || results[1].Error != "reviewer unavailable" {
		t.Fatalf("observations: %+v", results)
	}
}

func TestWrapperInfoClaude(t *testing.T) {
	wrapper := `{"is_error":false,"usage":{"input_tokens":4,"cache_creation_input_tokens":647,"cache_read_input_tokens":3882,"output_tokens":549,` +
		`"output_tokens_details":{"thinking_tokens":0},"cache_creation":{"ephemeral_1h_input_tokens":647},"iterations":[{"type":"message"}],"fallback_credit":null},` +
		`"modelUsage":{"claude-opus-5-5":{"inputTokens":4}},"structured_output":{}}`
	models, tokens := wrapperInfo([]byte(wrapper))
	if !slices.Equal(models, []string{"claude-opus-5-5"}) || tokens != (evalTokens{Input: 4, Output: 549, CacheRead: 3882, CacheCreation: 647}) {
		t.Fatalf("models %v tokens %+v", models, tokens)
	}
}

func TestWrapperInfoCodex(t *testing.T) {
	events := `{"type":"thread.started"}` + "\n" + `{"type":"turn.completed","usage":{"input_tokens":100,"cached_input_tokens":60,"output_tokens":9}}` + "\n"
	_, tokens := wrapperInfo([]byte(events))
	if tokens != (evalTokens{Input: 100, Output: 9, CacheRead: 60}) {
		t.Fatalf("tokens %+v", tokens)
	}
}

func TestReviewersExposeWrapperToCapture(t *testing.T) {
	wrapper := `{"is_error":false,"structured_output":` + okOut + `,"usage":{"input_tokens":1,"output_tokens":2}}`
	bin, _ := fake(t, "cat <<'X'\n"+wrapper+"\nX\n")
	var got []byte
	if _, _, err := (&Claude{Bin: bin, Capture: func(w []byte) { got = w }}).Run(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != wrapper {
		t.Fatalf("claude wrapper %q", got)
	}
	bin, _ = fake(t, codexBody)
	got = nil
	if _, _, err := (&Codex{Bin: bin, Capture: func(w []byte) { got = w }}).Run(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"type":"turn.completed"`) {
		t.Fatalf("codex events %q", got)
	}
}
