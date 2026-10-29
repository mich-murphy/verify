package glob

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		pattern, name string
		want          bool
	}{
		{"cmd/**", "cmd/verify/main.go", true},
		{"cmd/**", "cmd", true},
		{"cmd/**", "internal/cmd/x.go", false},
		{"**/*.md", "a/b/c.md", true},
		{"**/*.md", "c.md", true},
		{"**/*.md", "c.mdx", false},
		{"internal/state/**", "internal/state/store.go", true},
		{"internal/*/x.go", "internal/a/b/x.go", false},
		{"a/**/z", "a/z", true},
		{"a/**/z", "a/b/c/z", true},
		{"a/**/z", "a/b/c/y", false},
		{"go.mod", "go.mod", true},
		{"go.mod", "x/go.mod", false},
	}
	for _, tc := range tests {
		if got := Match(tc.pattern, tc.name); got != tc.want {
			t.Errorf("Match(%q,%q)=%v want %v", tc.pattern, tc.name, got, tc.want)
		}
	}
}

func TestValid(t *testing.T) {
	if !Valid("cmd/**") || Valid("") || Valid("a/[") {
		t.Fatal("unexpected Valid result")
	}
}
