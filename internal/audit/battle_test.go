package audit

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBattleWarningFlagDoesNotMeetErrorRequirement(t *testing.T) {
	for _, flag := range []string{"--report-unused-disable-directives-severity=warn", "--report-unused-disable-directives-severity=off", "--report-unused-disable-directives=false", "--report-unused-disable-directives-severity=error --report-unused-disable-directives-severity=warn", "--report-unused-disable-directives-severity=errorish"} {
		t.Run(flag, func(t *testing.T) {
			d := repo(t, map[string]string{"package.json": `{"scripts":{"lint":"oxlint ` + flag + `"}}`, "package-lock.json": "{}", "tsconfig.json": `{"compilerOptions":{"strict":true,"noUncheckedIndexedAccess":true}}`})
			a := &auditor{dir: d}
			a.tsFiles(".")
			if a.err != nil {
				t.Fatal(a.err)
			}
			if get(t, Report{Obligations: a.obs}, "lint-unused-directives").Satisfied {
				t.Fatalf("non-error setting %s satisfied required error obligation", flag)
			}
		})
	}
}

func FuzzBattleStripJSONC(f *testing.F) {
	for _, s := range []string{`{}`, `{"x":"// not a comment",}`, "{/* comment */\"x\":true,}", `"\\"`, `/*`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<16 {
			return
		}
		got := stripJSONC([]byte(input))
		if json.Valid([]byte(input)) {
			var before, after any
			if err := json.Unmarshal([]byte(input), &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(got, &after); err != nil {
				t.Fatalf("valid JSON corrupted: %v", err)
			}
			a, _ := json.Marshal(before)
			b, _ := json.Marshal(after)
			if !bytes.Equal(a, b) {
				t.Fatalf("JSON semantics changed: %s -> %s", a, b)
			}
		}
		if len(got) > len(input) {
			t.Fatalf("strip grew %d bytes to %d", len(input), len(got))
		}
	})
}
