package audit

import "testing"

func TestMakeRecipe(t *testing.T) {
	const mk = `.PHONY: cover test-hook

VAR := value

test-hook:
	go test -race ./...

cover: deps
	@echo start
	# go test -count=1 in a comment
	go test -count=1 \
		-shuffle=on ./...

a b: x
	echo shared

unrelated:
	go test -count=1 ./...
`
	for _, tc := range []struct{ name, text, target, want string }{
		{"simple", mk, "test-hook", "go test -race ./..."},
		{"deps, comments skipped, continuation joined", mk, "cover", "@echo start\ngo test -count=1  -shuffle=on ./..."},
		{"grouped targets", mk, "b", "echo shared"},
		{"last target", mk, "unrelated", "go test -count=1 ./..."},
		{"missing target", mk, "nope", ""},
		{"variable assignment is not a target", mk, "VAR", ""},
		{"empty makefile", "", "cover", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := MakeRecipe(tc.text, tc.target); got != tc.want {
				t.Fatalf("MakeRecipe(%q) = %q, want %q", tc.target, got, tc.want)
			}
		})
	}
}

func TestUncached(t *testing.T) {
	for cmd, want := range map[string]bool{
		"go test -count=1 ./...":              true,
		"go test -shuffle=on -race ./...":     true,
		"go test -race -count=10 ./...":       false,
		"go test -race ./...":                 false,
		"sh -c 'go test -count=1 ./... && x'": true,
	} {
		if got := Uncached(cmd); got != want {
			t.Errorf("Uncached(%q) = %v, want %v", cmd, got, want)
		}
	}
}
