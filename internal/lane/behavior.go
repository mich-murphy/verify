package lane

import (
	"path"
	"strings"
)

// IsBehavioral identifies known source/scripts and consumed build/deploy/test
// inputs, independently of location or executable bits. It does not inspect prose
// for quoted code or treat arbitrary images/data files as executable.
func IsBehavioral(c Change) bool {
	if c.OldMode == "100755" || c.NewMode == "100755" {
		return true
	}
	// Links and submodules are references to behavior, not plain documentation.
	for _, mode := range []string{c.OldMode, c.NewMode} {
		if mode == "120000" || mode == "160000" {
			return true
		}
	}
	if sourceExtensions[strings.ToLower(path.Ext(c.Path))] {
		return true
	}
	base := path.Base(c.Path)
	return IsManifestOrLockfile(c.Path) || buildInputs[base] || strings.HasPrefix(base, "Dockerfile.") || strings.HasPrefix(base, "Taskfile.")
}

var sourceExtensions = map[string]bool{
	".go": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".mdx": true, ".ipynb": true,
	".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true, ".bat": true, ".cmd": true,
	".py": true, ".pyi": true, ".rb": true, ".pl": true, ".php": true, ".lua": true,
	".rs": true, ".c": true, ".h": true, ".cc": true, ".cpp": true, ".cxx": true, ".hpp": true,
	".java": true, ".kt": true, ".kts": true, ".scala": true, ".swift": true, ".m": true, ".mm": true,
	".cs": true, ".fs": true, ".fsx": true, ".vb": true, ".r": true, ".ex": true, ".exs": true,
	".erl": true, ".hs": true, ".clj": true, ".cljs": true, ".dart": true,
	".vue": true, ".svelte": true, ".astro": true, ".html": true, ".htm": true, ".css": true, ".scss": true, ".sass": true, ".less": true,
	".sql": true, ".proto": true, ".graphql": true, ".gql": true,
	".mk": true, ".cmake": true, ".gradle": true, ".tf": true, ".tfvars": true, ".feature": true, ".snap": true,
}

var buildInputs = map[string]bool{
	"go.mod": true, "go.work": true, "package.json": true,
	"Makefile": true, "GNUmakefile": true, "makefile": true, "justfile": true, "Justfile": true,
	"Taskfile": true, "mise.toml": true, ".mise.toml": true, "CMakeLists.txt": true,
	"Dockerfile": true, "Containerfile": true, "compose.yaml": true, "compose.yml": true,
	"docker-compose.yaml": true, "docker-compose.yml": true,
	"Cargo.toml": true, "pyproject.toml": true, "requirements.txt": true, "setup.cfg": true,
	"Gemfile": true, "Rakefile": true, "Procfile": true, "BUILD": true, "BUILD.bazel": true, "WORKSPACE": true,
}
