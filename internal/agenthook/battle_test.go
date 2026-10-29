package agenthook

import (
	"strings"
	"testing"
)

func TestBattleSupportedYMLConfigProtected(t *testing.T) {
	v := Evaluate(strings.NewReader(`{"toolName":"edit","input":{"path":".pre-commit-config.yml"}}`))
	if !v.Block {
		t.Fatal("editing a supported prek config was allowed")
	}
}

func FuzzBattleEvaluate(f *testing.F) {
	for _, s := range []string{`{}`, `null`, `{"toolName":"bash","input":{"command":"git commit -n"}}`, `{"tool_name":"Write","tool_input":{"file_path":"verify.yaml"}}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > maxInput+16 {
			return
		}
		v := Evaluate(strings.NewReader(input))
		if v.Block && (v.Reason == "" || strings.Contains(v.Reason, "\n")) {
			t.Fatalf("bad block verdict: %+v", v)
		}
	})
}

func BenchmarkBattleEvaluate(b *testing.B) {
	input := `{"toolName":"bash","input":{"command":"go test ./... && git commit -m repair"}}`
	b.ReportAllocs()
	for b.Loop() {
		Evaluate(strings.NewReader(input))
	}
}
