// Package config loads and validates the optional verify.yaml described in SPEC §3.
package config

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/mich-murphy/verify/internal/gitx"
	"github.com/mich-murphy/verify/internal/glob"
)

// FileName is the repository-root configuration file.
const FileName = "verify.yaml"

// ErrInvalid marks configuration the user must fix (exit code 2).
var ErrInvalid = errors.New("invalid verify.yaml")

// Duration is a YAML duration string such as "10m".
type Duration time.Duration

// UnmarshalYAML parses a time.ParseDuration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Std converts to time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Fixed limits (SPEC §3): not configurable.
const (
	// SignoffLines is the changed-line count above which a change needs sign-off.
	SignoffLines = 400
	// ReviewCalls is the review budget per branch.
	ReviewCalls = 2
)

// Stages are the prek stages `verify check` runs, in order.
func Stages() []string { return []string{"pre-commit", "pre-push"} }

// Config is the parsed verify.yaml. Every key is optional; Base is filled in
// by Load when the file gives none.
type Config struct {
	Base         string   `yaml:"base"`
	Prepare      []Check  `yaml:"prepare"`
	SignoffPaths []string `yaml:"signoff_paths"`
	Verify       Verify   `yaml:"verify"`
	Waivers      []Waiver `yaml:"waivers"`
}

// Check is a command run in the worktree.
type Check struct {
	Name    string   `yaml:"name"`
	Run     []string `yaml:"run"`
	Timeout Duration `yaml:"timeout"`
}

// Verify configures CLI scenarios.
type Verify struct {
	Surfaces  []string   `yaml:"surfaces"`
	Setup     []string   `yaml:"setup"`
	Scenarios []Scenario `yaml:"scenarios"`
}

// Scenario is one CLI run with assertions.
type Scenario struct {
	Name           string   `yaml:"name"`
	Run            []string `yaml:"run"`
	Stdin          string   `yaml:"stdin"`
	Exit           int      `yaml:"exit"`
	StdoutContains *string  `yaml:"stdout_contains"`
	StdoutEquals   *string  `yaml:"stdout_equals"`
	Timeout        Duration `yaml:"timeout"`
}

// Waiver waives a setup obligation, with a reason.
type Waiver struct {
	Obligation string `yaml:"obligation"`
	Reason     string `yaml:"reason"`
}

// Default returns the configuration used when no verify.yaml exists.
func Default() Config { return Config{Base: "main"} }

// Parse strictly decodes and validates verify.yaml content. Unknown keys are errors.
func Parse(data []byte) (Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple YAML documents")
		}
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if err := c.validate(); err != nil {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return c, nil
}

func validRef(b string) bool {
	return b != "" && !strings.HasPrefix(b, "-") && !strings.ContainsAny(b, " \t\r\n\x00")
}

func (c *Config) validate() error {
	if c.Base != "" && !validRef(c.Base) {
		return fmt.Errorf("base %q is not a valid ref", c.Base)
	}
	if err := validatePrepare(c.Prepare); err != nil {
		return err
	}
	for _, p := range c.SignoffPaths {
		if !glob.Valid(p) {
			return fmt.Errorf("signoff_paths: bad pattern %q", p)
		}
	}
	if err := c.validateVerify(); err != nil {
		return err
	}
	for i, w := range c.Waivers {
		if w.Obligation == "" || strings.TrimSpace(w.Reason) == "" {
			return fmt.Errorf("waivers[%d]: obligation and reason are required", i)
		}
	}
	return nil
}

// validatePrepare validates prepare: entries and applies the default timeout.
func validatePrepare(cmds []Check) error {
	seen := map[string]bool{}
	for i := range cmds {
		ch := &cmds[i]
		if ch.Name == "" || seen[ch.Name] {
			return fmt.Errorf("prepare[%d]: name is required and must be unique", i)
		}
		seen[ch.Name] = true
		if !validArgv(ch.Run) {
			return fmt.Errorf("prepare[%d] %q: run must be a non-empty argv list", i, ch.Name)
		}
		if ch.Timeout < 0 {
			return fmt.Errorf("prepare[%d] %q: negative timeout", i, ch.Name)
		}
		if ch.Timeout == 0 {
			ch.Timeout = Duration(10 * time.Minute)
		}
	}
	return nil
}

func validArgv(argv []string) bool {
	if len(argv) == 0 || argv[0] == "" {
		return false
	}
	for _, arg := range argv {
		if strings.ContainsRune(arg, 0) {
			return false
		}
	}
	return true
}

func (c *Config) validateVerify() error {
	for _, p := range c.Verify.Surfaces {
		if !glob.Valid(p) {
			return fmt.Errorf("verify.surfaces: bad pattern %q", p)
		}
	}
	if len(c.Verify.Setup) > 0 && !validArgv(c.Verify.Setup) {
		return errors.New("verify.setup: invalid argv")
	}
	if len(c.Verify.Surfaces) > 0 && len(c.Verify.Scenarios) == 0 {
		return errors.New("verify.surfaces requires at least one scenario")
	}
	seen := map[string]bool{}
	for i := range c.Verify.Scenarios {
		s := &c.Verify.Scenarios[i]
		if s.Name == "" || seen[s.Name] {
			return fmt.Errorf("verify.scenarios[%d]: name is required and must be unique", i)
		}
		seen[s.Name] = true
		if !validArgv(s.Run) {
			return fmt.Errorf("verify.scenarios[%d] %q: run must be a non-empty argv list", i, s.Name)
		}
		if s.Exit < 0 || s.Exit > 255 {
			return fmt.Errorf("verify.scenarios[%d] %q: exit out of range", i, s.Name)
		}
		if s.Timeout < 0 {
			return fmt.Errorf("verify.scenarios[%d] %q: negative timeout", i, s.Name)
		}
		if s.Timeout == 0 {
			s.Timeout = Duration(time.Minute)
		}
	}
	return nil
}

// Waived reports whether a setup obligation is waived in config.
func (c Config) Waived(id string) bool {
	for _, w := range c.Waivers {
		if w.Obligation == id {
			return true
		}
	}
	return false
}

// Loaded is the result of Load.
type Loaded struct {
	Config        Config
	MergeBase     string // merge-base of the candidate and the base ref
	FromCandidate bool   // the merge-base had no usable verify.yaml, so the candidate's governs
	Found         bool   // some verify.yaml was used; false means defaults
}

// Load anchors policy at the default branch before considering candidate
// overrides. A valid trusted base config governs; absent/invalid base config
// permits a candidate override unless it would select itself as its base.
func Load(ctx context.Context, repo *gitx.Repo, sha string) (Loaded, error) {
	candidateOID, err := repo.Resolve(ctx, sha)
	if err != nil {
		return Loaded{}, err
	}
	cand, candFound, err := repo.FileAt(ctx, sha, FileName)
	if err != nil {
		return Loaded{}, err
	}
	derived := repo.DefaultBranch(ctx)
	anchor, err := mergeBase(ctx, repo, derived, sha)
	if err != nil {
		if candFound {
			return candidateConfig(ctx, repo, sha, candidateOID, "", derived, cand)
		}
		return Loaded{}, err
	}
	data, found, err := repo.FileAt(ctx, anchor, FileName)
	if err != nil {
		return Loaded{}, err
	}
	out := Loaded{MergeBase: anchor}
	if found {
		if c, perr := Parse(data); perr == nil {
			out.Config, out.Found = c, true
			out.Config.Base = cmp.Or(c.Base, derived)
			out.MergeBase, err = mergeBase(ctx, repo, out.Config.Base, sha)
			if err == nil && out.MergeBase == candidateOID && anchor != candidateOID {
				return Loaded{}, fmt.Errorf("%w: trusted base %q selects the candidate itself", ErrInvalid, out.Config.Base)
			}
			return out, err
		}
		// An invalid anchor config cannot authenticate candidate policy.
		out.FromCandidate = true
	}
	if candFound {
		return candidateConfig(ctx, repo, sha, candidateOID, anchor, derived, cand)
	}
	out.Config = Default()
	out.Config.Base = derived
	return out, nil
}

func candidateConfig(ctx context.Context, repo *gitx.Repo, sha, candidateOID, anchor, derived string, cand []byte) (Loaded, error) {
	c, err := Parse(cand)
	if err != nil {
		return Loaded{}, err
	}
	c.Base = cmp.Or(c.Base, derived)
	out := Loaded{Config: c, MergeBase: anchor, Found: true, FromCandidate: true}
	if anchor == "" && c.Base == derived {
		return Loaded{}, fmt.Errorf("%w: no independent base %q exists; configure an available target", ErrInvalid, derived)
	}
	if c.Base != derived {
		out.MergeBase, err = mergeBase(ctx, repo, c.Base, sha)
		if err != nil {
			return Loaded{}, err
		}
		if out.MergeBase == candidateOID && anchor != candidateOID {
			return Loaded{}, fmt.Errorf("%w: candidate base %q selects the candidate itself; configure an independent target (for example origin/HEAD)", ErrInvalid, c.Base)
		}
	}
	return out, nil
}

// PrekConfig selects the root config with prek's precedence. An absent config
// returns "", nil; other stat errors fail closed.
func PrekConfig(dir string) (string, error) {
	root, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("stat %s: %w", root, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", root)
	}
	for _, name := range []string{"prek.toml", ".pre-commit-config.yaml", ".pre-commit-config.yml"} {
		path := filepath.Join(root, name)
		info, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("prek config %s is not a regular file", path)
		}
		f, err := os.Open(path) // #nosec G304 -- fixed prek config filename under caller's repository directory
		if err != nil {
			return "", fmt.Errorf("read %s: %w", path, err)
		}
		if err := f.Close(); err != nil {
			return "", err
		}
		return path, nil
	}
	return "", nil
}

// mergeBase is the merge-base of sha with base or origin/<base>, whichever
// is newer, so a stale local base branch does not pull already-merged
// commits into the change.
func mergeBase(ctx context.Context, repo *gitx.Repo, base, sha string) (string, error) {
	var found []string
	var errs []error
	for _, ref := range []string{base, "origin/" + base} {
		mb, err := repo.MergeBase(ctx, ref, sha)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		found = append(found, mb)
	}
	switch {
	case len(found) == 0:
		return "", fmt.Errorf("merge-base of %q and %s: %w", base, sha, errors.Join(errs...))
	case len(found) == 1 || found[0] == found[1]:
		return found[0], nil
	}
	newer, err := repo.IsAncestor(ctx, found[0], found[1])
	if err != nil {
		return "", err
	}
	if newer {
		return found[1], nil
	}
	return found[0], nil
}
