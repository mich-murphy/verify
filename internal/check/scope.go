package check

import (
	"path"
	"strings"

	"github.com/mich-murphy/verify/internal/lane"
)

// Scope values recorded in a check record (SPEC §4.2).
const (
	ScopeFull     = "full"
	ScopeAffected = "affected"
)

var taskRunnerBases = map[string]bool{
	"Makefile": true, "GNUmakefile": true, "justfile": true, "Justfile": true,
	"Taskfile.yml": true, "Taskfile.yaml": true, "mise.toml": true,
}

// computeScope decides the blast radius from the changed paths. It is full
// when there is no merge-base or a path can affect everything.
func computeScope(mergeBase string, paths []string) (scope, reason string) {
	if mergeBase == "" {
		return ScopeFull, "no merge-base"
	}
	for _, p := range paths {
		base := path.Base(p)
		switch {
		case lane.IsManifestOrLockfile(p):
			return ScopeFull, "manifest or lockfile changed: " + p
		case lane.IsQualityConfig(p):
			return ScopeFull, "quality config changed: " + p
		case taskRunnerBases[base] || strings.HasSuffix(base, ".mk"):
			return ScopeFull, "task-runner file changed: " + p
		}
	}
	return ScopeAffected, ""
}
