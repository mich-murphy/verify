package agenthook

import (
	"strings"
	"testing"
)

func TestCheckCommand(t *testing.T) {
	block := []string{
		`git commit --no-verify -m x`,
		`git commit -m x --no-verify`,
		`git push --no-verify`,
		`git push origin main --no-verify`,
		`git commit --no-ver -m x`,
		`git commit -n -m x`,
		`git commit -nm x`,
		`git commit -anm x`,
		`git commit -m x -n`,
		`git -C /tmp commit -n`,
		`git -c user.name=x commit -n`,
		`cd x && git commit -n`,
		`echo hi; git commit --no-verify`,
		`/usr/bin/git commit -n`,
		`FOO=1 git commit -n`,
		`env git commit -n`,
		`sudo -n git commit -n`,
		`bash -c "git commit -n -m x"`,
		`sh -lc 'git commit --no-verify'`,
		`git -c core.hooksPath=/dev/null commit -m x`,
		`git config core.hooksPath /tmp/h`,
		`git config --global core.hookspath .`,
		`SKIP=lint git commit -m x`,
		`PREK_SKIP=lint git commit -m x`,
		`env SKIP=lint git commit -m x`,
		`export SKIP=all`,
		`prek uninstall`,
		`pre-commit uninstall`,
		`prek --quiet uninstall`,
		`rm .git/hooks/pre-commit`,
		`rm -rf .git/hooks`,
		`mv .git/hooks/pre-commit /tmp`,
		`chmod -x .git/hooks/pre-commit`,
		`echo x > .git/hooks/pre-commit`,
		`echo x >> .git/hooks/pre-push`,
		`echo x >.git/hooks/pre-push`,
		`cat foo | tee .git/hooks/pre-commit`,
		`sed -i s/a/b/ .git/hooks/pre-commit`,
		`find .git/hooks -delete`,
		`git commit -m "ok" && git push --no-verify`,
		`(git commit -n)`,
		"git commit \\\n  --no-verify",
		`git merge --no-verify x`,
		`git rebase --no-verify main`,
		`git cherry-pick --no-verify abc`,
		`git am --no-verify p.patch`,
		`git config hook.pre-commit.command true`,
		`git config --local core.hooksPath x`,
		`go test && git commit --no-verify`,
		`env SKIP=x git commit -m x`,
		`bash -c 'git commit --no-verify'`,
		`git commit -am x -n`,
	}
	for _, c := range block {
		t.Run("block "+c, func(t *testing.T) {
			if r := CheckCommand(c); r == "" {
				t.Errorf("allowed %q", c)
			} else if strings.Contains(r, "\n") {
				t.Errorf("reason not one line: %q", r)
			}
		})
	}
	allow := []string{
		``,
		`ls -la`,
		`git status`,
		`git commit -m "fix: n is not a flag" `,
		`git commit -m "use --no-verify never"`,
		`git commit -m '-n'`,
		`git commit -am "msg"`,
		`git commit --amend --no-edit`,
		`git commit -m x`,
		`git push origin main`,
		`git push -n`,
		`git push --dry-run`,
		`git log -n 5`,
		`git diff -n`,
		`git commit -F -n`,
		`git commit -- -n`,
		`echo "git commit -n"`,
		`echo SKIP=1`,
		`echo core.hooksPath`,
		`cat .git/hooks/pre-commit`,
		`ls .git/hooks`,
		`git config core.editor vim`,
		`prek run --all-files`,
		`prek install`,
		`rm foo.txt`,
		`FOO=1 git commit -m x`,
		`git commit -m x # --no-verify`,
		`go test ./... 2>&1 | tail`,
		`bash -c "echo hi"`,
		`bash script.sh`,
		`git -C dir status`,
		`git commit --message=-n`,
		`git commit -m x 2>&1`,
		`git commit -m x &> /dev/null`,
		`git commit --fixup HEAD`,
		`unterminated "quote`,
		`git config --get core.hooksPath`,
		`git merge --no-ff x`,
		`git rebase main`,
		`git commit -m "a -n b"`,
		`git commit -am "-n"`,
		`go test ./... && git commit -m x`,
	}
	for _, c := range allow {
		t.Run("allow "+c, func(t *testing.T) {
			if r := CheckCommand(c); r != "" {
				t.Errorf("blocked %q: %s", c, r)
			}
		})
	}
}

func TestCheckPath(t *testing.T) {
	block := []string{
		"verify.yaml", "./verify.yaml", "sub/verify.yaml", "/abs/repo/verify.yaml",
		".pre-commit-config.yaml", "prek.toml", "lefthook.yml",
		".husky/pre-commit", ".husky/_/husky.sh", "x/.husky/commit-msg", ".git/config", "/r/.git/config",
		".git/hooks/pre-commit", "/r/.git/hooks/pre-push", "a/../verify.yaml",
	}
	for _, p := range block {
		if CheckPath(p) == "" {
			t.Errorf("allowed %q", p)
		}
	}
	allow := []string{"", "main.go", "docs/verify.yaml.md", "verify.yml", ".githooks/x", "husky/x", "internal/hooks/a.go"}
	for _, p := range allow {
		if r := CheckPath(p); r != "" {
			t.Errorf("blocked %q: %s", p, r)
		}
	}
}

func TestEvaluate(t *testing.T) {
	tests := []struct {
		name, in      string
		block, warned bool
	}{
		{"pi bash block", `{"toolName":"bash","input":{"command":"git commit --no-verify"}}`, true, false},
		{"pi bash ok", `{"toolName":"bash","input":{"command":"git status"}}`, false, false},
		{"pi write block", `{"toolName":"write","input":{"path":"verify.yaml","content":"x"}}`, true, false},
		{"pi edit block", `{"toolName":"edit","input":{"path":".husky/pre-commit"}}`, true, false},
		{"pi write ok", `{"toolName":"write","input":{"path":"main.go"}}`, false, false},
		{"cc Bash block", `{"tool_name":"Bash","tool_input":{"command":"git commit -n"}}`, true, false},
		{"cc Bash ok", `{"tool_name":"Bash","tool_input":{"command":"ls"}}`, false, false},
		{"cc Write block", `{"tool_name":"Write","tool_input":{"file_path":"/r/prek.toml"}}`, true, false},
		{"cc Edit block", `{"tool_name":"Edit","tool_input":{"file_path":".git/hooks/pre-commit"}}`, true, false},
		{"cc MultiEdit block", `{"tool_name":"MultiEdit","tool_input":{"file_path":"lefthook.yml"}}`, true, false},
		{"cc Write ok", `{"tool_name":"Write","tool_input":{"file_path":"README.md"}}`, false, false},
		{"extra fields", `{"tool_name":"Bash","session_id":"s","hook_event_name":"PreToolUse","tool_input":{"command":"ls","description":"d"}}`, false, false},
		{"unknown tool", `{"toolName":"read","input":{"path":"verify.yaml"}}`, false, false},
		{"no input", `{"toolName":"bash"}`, false, false},
		{"malformed", `{not json`, false, true},
		{"empty", ``, false, true},
		{"no name", `{"input":{}}`, false, true},
		{"bad input", `{"toolName":"bash","input":"str"}`, false, true},
		{"array", `[]`, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := Evaluate(strings.NewReader(tc.in))
			if v.Block != tc.block {
				t.Errorf("block = %v, want %v (%s)", v.Block, tc.block, v.Reason)
			}
			if (v.Warning != "") != tc.warned {
				t.Errorf("warning = %q, want warned=%v", v.Warning, tc.warned)
			}
			if v.Block && v.Reason == "" {
				t.Error("blocked without reason")
			}
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, strings.NewReader("").UnreadByte() }

func TestEvaluateReadError(t *testing.T) {
	v := Evaluate(errReader{})
	if v.Block || v.Warning == "" {
		t.Errorf("got %+v", v)
	}
}
