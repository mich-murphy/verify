#!/bin/sh
# Check formatting of the Go files passed by prek (fast tier, SPEC §4.5).
# With no arguments, check every tracked Go file.
set -eu
if [ "$#" -eq 0 ]; then
	set -- $(git ls-files '*.go')
fi
out="$(go tool -modfile=tools/go.mod gofumpt -l "$@")"
imports="$(go tool -modfile=tools/go.mod goimports -l "$@")"
if [ -n "$out$imports" ]; then
	echo "needs formatting (run make fmt):"
	printf '%s\n' "$out" "$imports"
	exit 1
fi
