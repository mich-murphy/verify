// Package state persists Verify records under <git-common-dir>/verify/.
// Writes are atomic and made under an exclusive lock; reads fail closed.
package state

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

// ErrCorrupt marks a record that exists but cannot be trusted.
var ErrCorrupt = errors.New("corrupt verify state")

// ErrNotFound is returned when a record does not exist.
var ErrNotFound = errors.New("verify state not found")

var shaPattern = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// Store is the state directory.
type Store struct{ Dir string }

// Open returns the store rooted at <commonDir>/verify without creating it.
func Open(commonDir string) *Store { return &Store{Dir: filepath.Join(commonDir, "verify")} }

// WorktreesDir is where Verify-owned detached worktrees live.
func (s *Store) WorktreesDir() string { return filepath.Join(s.Dir, "worktrees") }

// RunDir is runs/<sha>.
func (s *Store) RunDir(sha string) (string, error) {
	if !shaPattern.MatchString(sha) {
		return "", fmt.Errorf("invalid sha %q", sha)
	}
	return filepath.Join(s.Dir, "runs", sha), nil
}

// LogPath is runs/<sha>/logs/<step>.log with the step name made path-safe.
func (s *Store) LogPath(sha, step string) (string, error) {
	d, err := s.RunDir(sha)
	if err != nil {
		return "", err
	}
	if step == "" {
		return "", fmt.Errorf("step name required")
	}
	name := url.PathEscape(step)
	return filepath.Join(d, "logs", name+".log"), nil
}

// BranchPath is branches/<branch>.json; slashes in branch names are escaped.
func (s *Store) BranchPath(branch string) string {
	safe := strings.NewReplacer("%", "%25", "/", "%2F", "\\", "%5C", "\x00", "%00").Replace(branch)
	if safe == "" || safe == "." || safe == ".." {
		safe = "_" + safe
	}
	return filepath.Join(s.Dir, "branches", safe+".json")
}

// Lock takes the exclusive store lock and returns the unlock function.
func (s *Store) Lock() (func(), error) { return LockFile(filepath.Join(s.Dir, "lock")) }

// LockFile takes an exclusive flock on path (creating it and its directory)
// and returns the unlock function. It blocks until the lock is free.
func LockFile(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- Verify-owned lock path.
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { // #nosec G115 -- fd fits int.
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) // #nosec G115 -- fd fits int.
		_ = f.Close()
	}, nil
}

// WriteAtomic writes data to path via temp file, fsync, rename and directory fsync.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	cleanup := func(e error) error { _ = tmp.Close(); _ = os.Remove(name); return e }
	if _, err = tmp.Write(data); err != nil {
		return cleanup(err)
	}
	if err = tmp.Chmod(0o600); err != nil {
		return cleanup(err)
	}
	if err = tmp.Sync(); err != nil {
		return cleanup(err)
	}
	if err = tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err = os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	d, err := os.Open(dir) //nolint:gosec // dir derived inside state dir
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(path, append(data, '\n'))
}

// readJSON strictly decodes path into v and checks the schema field.
func readJSON(path, schema string, v any, schemaOf func() string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- path is derived inside the state dir.
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotFound, path)
	}
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrCorrupt, path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %s: %w; delete the file to reset", ErrCorrupt, path, err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: %s: trailing data", ErrCorrupt, path)
	}
	if got := schemaOf(); got != schema {
		return fmt.Errorf("%w: %s: schema %q, want %q", ErrCorrupt, path, got, schema)
	}
	return nil
}

// SaveCheck writes runs/<sha>/check.json.
func (s *Store) SaveCheck(r *CheckRecord) error {
	d, err := s.RunDir(r.SHA)
	if err != nil {
		return err
	}
	r.Schema = SchemaCheck
	if err := validateCheck(r); err != nil {
		return err
	}
	return s.locked(func() error { return writeJSON(filepath.Join(d, "check.json"), r) })
}

// LoadCheck reads the check record for sha.
func (s *Store) LoadCheck(sha string) (*CheckRecord, error) {
	d, err := s.RunDir(sha)
	if err != nil {
		return nil, err
	}
	var r CheckRecord
	if err := readJSON(filepath.Join(d, "check.json"), SchemaCheck, &r, func() string { return r.Schema }); err != nil {
		return nil, err
	}
	if r.SHA != sha {
		return nil, fmt.Errorf("%w: check record sha mismatch", ErrCorrupt)
	}
	if err := validateCheck(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SaveReview writes runs/<sha>/review.json.
func (s *Store) SaveReview(r *ReviewRecord) error {
	d, err := s.RunDir(r.SHA)
	if err != nil {
		return err
	}
	r.Schema = SchemaReview
	if err := validateReview(r); err != nil {
		return err
	}
	return s.locked(func() error { return writeJSON(filepath.Join(d, "review.json"), r) })
}

// LoadReview reads the review record for sha.
func (s *Store) LoadReview(sha string) (*ReviewRecord, error) {
	d, err := s.RunDir(sha)
	if err != nil {
		return nil, err
	}
	var r ReviewRecord
	if err := readJSON(filepath.Join(d, "review.json"), SchemaReview, &r, func() string { return r.Schema }); err != nil {
		return nil, err
	}
	if r.SHA != sha {
		return nil, fmt.Errorf("%w: review record sha mismatch", ErrCorrupt)
	}
	if err := validateReview(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// SaveVerify writes runs/<sha>/verify.json.
func (s *Store) SaveVerify(r *VerifyRecord) error {
	d, err := s.RunDir(r.SHA)
	if err != nil {
		return err
	}
	r.Schema = SchemaVerify
	if err := validateVerify(r); err != nil {
		return err
	}
	return s.locked(func() error { return writeJSON(filepath.Join(d, "verify.json"), r) })
}

// LoadVerify reads the verify record for sha.
func (s *Store) LoadVerify(sha string) (*VerifyRecord, error) {
	d, err := s.RunDir(sha)
	if err != nil {
		return nil, err
	}
	var r VerifyRecord
	if err := readJSON(filepath.Join(d, "verify.json"), SchemaVerify, &r, func() string { return r.Schema }); err != nil {
		return nil, err
	}
	if r.SHA != sha {
		return nil, fmt.Errorf("%w: verify record sha mismatch", ErrCorrupt)
	}
	if err := validateVerify(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// LoadBranch reads the branch state; a missing file yields a fresh Branch.
func (s *Store) LoadBranch(name string) (*Branch, error) {
	var b Branch
	err := readJSON(s.BranchPath(name), SchemaBranch, &b, func() string { return b.Schema })
	if errors.Is(err, ErrNotFound) {
		return &Branch{Schema: SchemaBranch, Name: name}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := validateBranch(&b, name); err != nil {
		return nil, err
	}
	return &b, nil
}

// UpdateBranch loads, mutates and saves a branch under one lock hold.
func (s *Store) UpdateBranch(name string, fn func(*Branch) error) error {
	return s.locked(func() error {
		b, err := s.LoadBranch(name)
		if err != nil {
			return err
		}
		if err := fn(b); err != nil {
			return err
		}
		b.Schema = SchemaBranch
		if err := validateBranch(b, name); err != nil {
			return err
		}
		return writeJSON(s.BranchPath(name), b)
	})
}

// NextFindingID returns the next F<n> for the branch's findings.
func (b *Branch) NextFindingID() string {
	n := 0
	for _, f := range b.Findings {
		var k int
		if _, err := fmt.Sscanf(f.ID, "F%d", &k); err == nil && k > n {
			n = k
		}
	}
	return fmt.Sprintf("F%d", n+1)
}

// locked runs fn holding the store lock. The lock is not re-entrant, so
// callers inside fn must use the unlocked helpers.
func (s *Store) locked(fn func() error) error {
	unlock, err := s.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}
