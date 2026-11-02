package review

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Expect is the scored answer for one case. Either Findings is "none" (a
// silent case) or Path, Kind, Blocking and Repro describe the one finding the
// reviewer must report. Resolved is the exact set of open IDs the diff fixes.
type Expect struct {
	Path     string   `json:"path,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Blocking bool     `json:"blocking,omitempty"`
	Repro    bool     `json:"repro,omitempty"`
	Findings string   `json:"findings,omitempty"`
	Resolved []string `json:"resolved"`
}

func (e Expect) silent() bool { return e.Findings == "none" }

func (e Expect) validate() error {
	switch {
	case e.Resolved == nil:
		return errors.New("resolved is required (use [] for none)")
	case e.silent():
		if e.Path != "" || e.Kind != "" || e.Blocking || e.Repro {
			return errors.New(`findings "none" excludes path, kind, blocking and repro`)
		}
	case e.Findings != "":
		return fmt.Errorf("findings must be \"none\" or omitted, got %q", e.Findings)
	case e.Path == "" || !slices.Contains(Kinds, e.Kind):
		return fmt.Errorf("a required finding needs a path and a known kind, got %q %q", e.Path, e.Kind)
	case e.Blocking && (e.Kind != "correctness" || !e.Repro):
		return errors.New("only a correctness finding with a repro can block")
	}
	return nil
}

// corpusCase is one testdata/review/<id>.json manifest. Why is for humans and
// is never sent to a reviewer.
type corpusCase struct {
	ID, Path, BeforeSHA256, AfterSHA256, OracleSHA256, Why string
	BeforePass, AfterPass                                  bool
	Extra                                                  []extraFile
	Expect                                                 Expect
	Input                                                  Input
}

// extraFile is another changed file in the same package, stored as
// before.<Path> and after.<Path> next to the primary fixture.
type extraFile struct{ Path, BeforeSHA256, AfterSHA256 string }

func (c corpusCase) diffPaths() []string {
	paths := []string{c.Path}
	for _, e := range c.Extra {
		paths = append(paths, e.Path)
	}
	return paths
}

// splitDiff cuts a multi-file diff into one section per changed path.
func splitDiff(diff string) map[string]string {
	sections := map[string]string{}
	var path string
	for line := range strings.SplitAfterSeq(diff, "\n") {
		if rest, ok := strings.CutPrefix(line, "diff --git a/"); ok {
			path, _, _ = strings.Cut(rest, " b/")
		}
		sections[path] += line
	}
	return sections
}

// fixtureBytes limits reads to the corpus tree; a manifest cannot name a path
// outside the root, including through symlinks.
func fixtureBytes(t *testing.T, path string) []byte {
	t.Helper()
	root, err := os.OpenRoot(filepath.Join("..", "..", "testdata", "review"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Errorf("close fixture root: %v", err)
		}
	}()
	file, err := root.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Errorf("close fixture file: %v", err)
		}
	}()
	data, err := readFixture(file)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readFixture(r io.Reader) ([]byte, error) {
	const maxFixture = 64 << 10
	data, err := io.ReadAll(io.LimitReader(r, maxFixture+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxFixture {
		return nil, fmt.Errorf("fixture exceeds 64 KiB")
	}
	return data, nil
}

func TestFixtureReadLimit(t *testing.T) {
	data, err := readFixture(strings.NewReader(strings.Repeat("x", 64<<10)))
	if err != nil || len(data) != 64<<10 {
		t.Fatalf("maximum fixture: %d bytes, error %v", len(data), err)
	}
	if _, err := readFixture(strings.NewReader(strings.Repeat("x", (64<<10)+1))); err == nil {
		t.Fatal("oversized fixture was accepted")
	}
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	manifests, err := filepath.Glob(filepath.Join("..", "..", "testdata", "review", "*.json"))
	if err != nil || len(manifests) == 0 {
		t.Fatalf("no corpus manifests: %v", err)
	}
	cases := make([]corpusCase, 0, len(manifests))
	for _, manifest := range manifests {
		id := strings.TrimSuffix(filepath.Base(manifest), ".json")
		dec := json.NewDecoder(bytes.NewReader(fixtureBytes(t, id+".json")))
		dec.DisallowUnknownFields()
		var c corpusCase
		if err := dec.Decode(&c); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if err := c.Expect.validate(); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if c.ID != id {
			t.Fatalf("case ID %q differs from filename %q", c.ID, id)
		}
		cases = append(cases, c)
	}
	return cases
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// replayDiff applies every hunk to the exact original source, checking line
// positions, context, counts, and all unchanged lines between hunks.
func replayDiff(diff, path string, before []byte) ([]byte, error) {
	if !strings.HasSuffix(string(before), "\n") || !strings.HasSuffix(diff, "\n") {
		return nil, fmt.Errorf("source and diff require final newlines")
	}
	lines := strings.Split(strings.TrimSuffix(diff, "\n"), "\n")
	if len(lines) < 5 || lines[0] != "diff --git a/"+path+" b/"+path || lines[1] != "--- a/"+path || lines[2] != "+++ b/"+path {
		return nil, fmt.Errorf("bad diff headers")
	}
	source := strings.Split(strings.TrimSuffix(string(before), "\n"), "\n")
	var result []string
	oldPos, changed, hunks := 0, 0, 0
	for i := 3; i < len(lines); {
		m := hunkHeader.FindStringSubmatch(lines[i])
		if m == nil {
			return nil, fmt.Errorf("bad hunk header at %d", i+1)
		}
		oldStart, _ := strconv.Atoi(m[1])
		newStart, _ := strconv.Atoi(m[3])
		oldCount, newCount := 1, 1
		if m[2] != "" {
			oldCount, _ = strconv.Atoi(m[2])
		}
		if m[4] != "" {
			newCount, _ = strconv.Atoi(m[4])
		}
		start := oldStart - 1
		if oldCount == 0 {
			start = oldStart
		}
		if start < oldPos || start > len(source) || newStart-1 != len(result)+start-oldPos {
			return nil, fmt.Errorf("hunk offset mismatch")
		}
		result = append(result, source[oldPos:start]...)
		oldPos = start
		i++
		oldSeen, newSeen := 0, 0
		for i < len(lines) && !strings.HasPrefix(lines[i], "@@ ") {
			line := lines[i]
			if len(line) < 1 {
				return nil, fmt.Errorf("empty hunk line")
			}
			switch line[0] {
			case ' ':
				if oldPos >= len(source) || source[oldPos] != line[1:] {
					return nil, fmt.Errorf("context mismatch")
				}
				oldPos++
				oldSeen++
				newSeen++
				result = append(result, line[1:])
			case '-':
				if oldPos >= len(source) || source[oldPos] != line[1:] {
					return nil, fmt.Errorf("deletion mismatch")
				}
				oldPos++
				oldSeen++
				changed++
			case '+':
				result = append(result, line[1:])
				newSeen++
				changed++
			default:
				return nil, fmt.Errorf("bad hunk line")
			}
			i++
		}
		if oldSeen != oldCount || newSeen != newCount {
			return nil, fmt.Errorf("hunk count mismatch")
		}
		hunks++
	}
	if hunks == 0 || changed == 0 {
		return nil, fmt.Errorf("no changes")
	}
	result = append(result, source[oldPos:]...)
	return []byte(strings.Join(result, "\n") + "\n"), nil
}

func sourceFile(t *testing.T, id, name, wantHash string) []byte {
	t.Helper()
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(wantHash) {
		t.Fatalf("invalid SHA-256 for %s", name)
	}
	data := fixtureBytes(t, filepath.Join(id, name))
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != wantHash {
		t.Fatalf("%s content hash differs", name)
	}
	return data
}

func runOracle(t *testing.T, sources map[string][]byte, oracle []byte, wantPass bool) {
	t.Helper()
	dir := t.TempDir()
	files := map[string][]byte{"oracle_test.go": oracle}
	maps.Copy(files, sources)
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-count=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "GOWORK=off")
	output, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("oracle timeout: %v", ctx.Err())
	}
	if wantPass && err != nil || !wantPass && (err == nil || !strings.Contains(string(output), "--- FAIL:")) {
		t.Fatalf("oracle pass=%t, want %t; error=%v; output=%s", err == nil, wantPass, err, output)
	}
}

// oracleMatchesExpect: a required finding means the change breaks the oracle
// (which passed before, unless an open finding already described the bug); a
// silent case may fix or preserve behavior but never break it.
func oracleMatchesExpect(c corpusCase) bool {
	if c.Expect.silent() {
		return c.AfterPass || !c.BeforePass
	}
	return !c.AfterPass && (c.BeforePass || len(c.Input.Open) > 0)
}

func validateCorpus(t *testing.T) []corpusCase {
	t.Helper()
	cases := loadCorpus(t)
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			if c.Path != "fixture.go" || c.Why == "" {
				t.Fatal("missing source or ground truth")
			}
			if !oracleMatchesExpect(c) {
				t.Fatal("oracle verdicts do not match the expected behavior")
			}
			open := openIDs(c.Input)
			for _, id := range c.Expect.Resolved {
				if !slices.Contains(open, id) {
					t.Fatalf("expected resolved %s is not open", id)
				}
			}
			before := map[string][]byte{c.Path: sourceFile(t, c.ID, "before.go", c.BeforeSHA256)}
			after := map[string][]byte{c.Path: sourceFile(t, c.ID, "after.go", c.AfterSHA256)}
			for _, e := range c.Extra {
				before[e.Path] = sourceFile(t, c.ID, "before."+e.Path, e.BeforeSHA256)
				after[e.Path] = sourceFile(t, c.ID, "after."+e.Path, e.AfterSHA256)
			}
			oracle := sourceFile(t, c.ID, "oracle_test.go", c.OracleSHA256)
			sections := splitDiff(c.Input.Diff)
			if len(sections) != len(before) {
				t.Fatalf("diff touches %d paths, manifest lists %d", len(sections), len(before))
			}
			for path, source := range before {
				if string(source) == string(after[path]) {
					t.Fatalf("%s unchanged", path)
				}
				applied, err := replayDiff(sections[path], path, source)
				if err != nil || string(applied) != string(after[path]) {
					t.Fatalf("diff does not produce after source for %s: %v", path, err)
				}
			}
			packet, err := Build(c.Input)
			if err != nil || len(packet) > 8192 {
				t.Fatalf("packet size/build: %d bytes: %v", len(packet), err)
			}
			expect, err := json.Marshal(c.Expect)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{c.Why, string(expect), c.BeforeSHA256, c.AfterSHA256, c.OracleSHA256, "oracle_test.go", "=== GROUND TRUTH ==="} {
				if strings.Contains(string(packet), secret) {
					t.Fatalf("ground truth or oracle leaked into reviewer packet")
				}
			}
			runOracle(t, before, oracle, c.BeforePass)
			runOracle(t, after, oracle, c.AfterPass)
		})
	}
	if t.Failed() {
		t.Fatal("corpus validation failed; refusing live evaluation")
	}
	return cases
}

func TestReviewCorpusIntegrity(t *testing.T) { validateCorpus(t) }

func TestReplayDiffRejectsWrongSourceAndHunks(t *testing.T) {
	before := []byte("package fixture\nvar value = 1\n")
	diff := "diff --git a/fixture.go b/fixture.go\n--- a/fixture.go\n+++ b/fixture.go\n@@ -1,2 +1,2 @@\n package fixture\n-var value = 1\n+var value = 2\n"
	if got, err := replayDiff(diff, "fixture.go", before); err != nil || string(got) != "package fixture\nvar value = 2\n" {
		t.Fatalf("valid diff: %q, %v", got, err)
	}
	for _, tc := range []struct {
		name, diff string
		before     []byte
	}{
		{"wrong source", diff, []byte("package fixture\nvar value = 3\n")},
		{"wrong line", strings.Replace(diff, "@@ -1,2 +1,2 @@", "@@ -2,2 +1,2 @@", 1), before},
		{"wrong count", strings.Replace(diff, "@@ -1,2 +1,2 @@", "@@ -1,3 +1,2 @@", 1), before},
		{"wrong path", strings.Replace(diff, "--- a/fixture.go", "--- a/other.go", 1), before},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := replayDiff(tc.diff, "fixture.go", tc.before); err == nil {
				t.Fatal("invalid diff applied")
			}
		})
	}
}
