package review

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	reviewEval       = flag.String("review-eval", "", "opt-in subscription reviewer: codex or claude")
	reviewEvalOutput = flag.String("review-eval-output", "", "new JSON output file (required for live evaluation)")
	reviewEvalCase   = flag.String("review-eval-case", "", "one case ID, or all eight when empty")
)

type corpusCase struct {
	ID, Path, BeforeSHA256, AfterSHA256, OracleSHA256, Truth, Expect string
	BeforePass, AfterPass                                            bool
	Input                                                            Input
}

var corpusIDs = []string{
	"stale-usage-reset", "excess-cache-markers", "authorization-removal", "optout-redirect",
	"repair-open-f1", "clean-correctness-fix", "accepted-unchanged", "resolved-recurrence",
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
	cases := make([]corpusCase, 0, len(corpusIDs))
	for _, id := range corpusIDs {
		data := fixtureBytes(t, id+".json")
		var c corpusCase
		if err := json.Unmarshal(data, &c); err != nil {
			t.Fatal(err)
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

func runOracle(t *testing.T, source, oracle []byte, wantPass bool) {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string][]byte{"fixture.go": source, "oracle_test.go": oracle} {
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

func validateCorpus(t *testing.T) []corpusCase {
	t.Helper()
	cases := loadCorpus(t)
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			if c.Path != "fixture.go" || c.Truth == "" || c.Expect == "" {
				t.Fatal("missing source or ground truth")
			}
			wantBefore, wantAfter := true, false
			if c.ID == "repair-open-f1" || c.ID == "clean-correctness-fix" || c.ID == "accepted-unchanged" {
				wantBefore, wantAfter = false, true
			}
			if c.BeforePass != wantBefore || c.AfterPass != wantAfter {
				t.Fatal("oracle verdicts do not match behavioral category")
			}
			before := sourceFile(t, c.ID, "before.go", c.BeforeSHA256)
			after := sourceFile(t, c.ID, "after.go", c.AfterSHA256)
			oracle := sourceFile(t, c.ID, "oracle_test.go", c.OracleSHA256)
			if string(before) == string(after) {
				t.Fatal("unchanged source")
			}
			applied, err := replayDiff(c.Input.Diff, c.Path, before)
			if err != nil || string(applied) != string(after) {
				t.Fatalf("diff does not produce after source: %v", err)
			}
			if c.ID == "repair-open-f1" && (len(c.Input.Open) != 1 || c.Input.Open[0].ID != "F1" || len(c.Input.Prior) != 0) {
				t.Fatal("repair requires open F1")
			}
			if c.ID == "accepted-unchanged" && (len(c.Input.Prior) != 1 || c.Input.Prior[0].State != "accepted") {
				t.Fatal("accepted finding missing")
			}
			if c.ID == "resolved-recurrence" && (len(c.Input.Prior) != 1 || c.Input.Prior[0].State != "resolved") {
				t.Fatal("resolved finding missing")
			}
			packet, err := Build(c.Input)
			if err != nil || len(packet) > 8192 {
				t.Fatalf("packet size/build: %d bytes: %v", len(packet), err)
			}
			for _, secret := range []string{c.Truth, c.Expect, c.BeforeSHA256, c.AfterSHA256, c.OracleSHA256, "oracle_test.go", "=== GROUND TRUTH ==="} {
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

type evaluationObservation struct {
	Case, Reviewer, RequestedModel, PromptSHA256, PacketSHA256, Raw, Duration string
	Usage                                                                     Usage
	Error                                                                     string `json:"error,omitempty"`
}

// TestReviewLiveEvaluation does not invoke any reviewer without explicit flags.
func TestReviewLiveEvaluation(t *testing.T) {
	if *reviewEval == "" {
		t.Log("live review disabled; supply -review-eval=codex|claude and -review-eval-output=NEW_FILE")
		return
	}
	if *reviewEvalOutput == "" || (*reviewEval != "codex" && *reviewEval != "claude") {
		t.Fatal("live review requires -review-eval=codex|claude and -review-eval-output=NEW_FILE")
	}
	claudeModel := cmp.Or(os.Getenv("GATE_CLAUDE_MODEL"), "opus")
	codexModel := os.Getenv("GATE_CODEX_MODEL")
	reviewer, err := Select(*reviewEval, "", claudeModel, codexModel)
	if err != nil {
		t.Fatal(err)
	}
	model := cmp.Or(codexModel, "CLI default")
	if *reviewEval == "claude" {
		model = claudeModel
	}
	type prepared struct {
		c      corpusCase
		packet []byte
	}
	var cases []prepared
	for _, c := range validateCorpus(t) {
		if *reviewEvalCase != "" && c.ID != *reviewEvalCase {
			continue
		}
		packet, buildErr := Build(c.Input)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		cases = append(cases, prepared{c, packet})
	}
	if len(cases) == 0 {
		t.Fatalf("unknown case %q", *reviewEvalCase)
	}
	// Reserve and test the destination BEFORE any charged calls.
	out, err := os.OpenFile(*reviewEvalOutput, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	var results []evaluationObservation
	persist := func() {
		t.Helper()
		encoded, encodeErr := json.MarshalIndent(results, "", "  ")
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if err := out.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := out.Seek(0, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := out.Write(append(encoded, '\n')); err != nil {
			t.Fatal(err)
		}
		if err := out.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	persist()
	for _, p := range cases {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
		start := time.Now()
		raw, usage, runErr := reviewer.Run(ctx, p.packet)
		cancel()
		packetSum := sha256.Sum256(p.packet)
		promptSum := sha256.Sum256(append([]byte(SystemPrompt+"\n\n=== PACKET ===\n"), p.packet...))
		obs := evaluationObservation{
			Case: p.c.ID, Reviewer: reviewer.Name(), RequestedModel: model,
			PromptSHA256: hex.EncodeToString(promptSum[:]), PacketSHA256: hex.EncodeToString(packetSum[:]),
			Raw: string(raw), Duration: time.Since(start).String(), Usage: usage,
		}
		if runErr != nil {
			obs.Error = runErr.Error()
		} else {
			open := make([]string, 0, len(p.c.Input.Open))
			for _, f := range p.c.Input.Open {
				open = append(open, f.ID)
			}
			if _, parseErr := Parse(raw, open, []string{p.c.Path}); parseErr != nil {
				obs.Error = fmt.Sprintf("response validation: %v", parseErr)
			}
		}
		results = append(results, obs)
		persist()
		if obs.Error != "" {
			t.Errorf("%s: %s (raw response saved)", p.c.ID, obs.Error)
		}
	}
}
