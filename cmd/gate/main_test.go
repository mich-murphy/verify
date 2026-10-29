package main

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestRunHelp(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"--help"}, strings.NewReader(""), &out, &errb); code != 0 || !strings.Contains(out.String(), "usage") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestMainExits(t *testing.T) {
	got := -1
	exit = func(c int) { got = c }
	defer func() { exit = os.Exit }()
	old := os.Args
	os.Args = []string{"gate", "--help"}
	defer func() { os.Args = old }()
	main()
	if got != 0 {
		t.Fatalf("exit %d", got)
	}
}
