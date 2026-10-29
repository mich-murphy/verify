// Package runner executes one foreground command with bounded output and group cleanup.
package runner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Command is an argv invocation with explicit directory, environment and stdin.
type Command struct {
	Args    []string
	Dir     string
	Env     map[string]string
	Stdin   []byte
	Timeout time.Duration
}

const ForegroundOwnerEnv = "VERIFY_FOREGROUND_OWNER"

type Status string

const (
	Pass      Status = "pass"
	Fail      Status = "fail"
	Cancelled Status = "cancelled"
	TimedOut  Status = "timedout"
)

// Config selects the controlled environment, output bound and cleanup budget.
type Config struct {
	Env           map[string]string
	Inherit       []string
	OutputLimit   int
	KillGrace     time.Duration
	SettleTimeout time.Duration
}

type Evidence struct {
	Output    string
	Truncated bool
	Bytes     int64
}

// Started distinguishes a process-start failure from a real nonzero exit.
// Settled=false requires callers to quarantine the command's worktree.
type Result struct {
	Status            Status
	Started, Finished time.Time
	ExitCode          int
	Error             string
	// ProcessError preserves an ordinary start/wait error or cancellation cause.
	// It is cleared for descendant/cleanup failures, which are not ordinary exits.
	ProcessError   error
	Stdout, Stderr Evidence
	Settled        bool
}

type Runner struct {
	config      Config
	process     processOps
	quarantined atomic.Bool
}

func New(config Config) (*Runner, error) {
	if config.OutputLimit < 0 || config.KillGrace < 0 || config.SettleTimeout < 0 {
		return nil, errors.New("negative runner limit")
	}
	if config.OutputLimit == 0 {
		config.OutputLimit = 1 << 20
	}
	if config.KillGrace == 0 {
		config.KillGrace = 250 * time.Millisecond
	}
	if config.SettleTimeout == 0 {
		config.SettleTimeout = 3 * time.Second
	}
	config.Env = copyEnv(config.Env)
	config.Inherit = append([]string(nil), config.Inherit...)
	if _, err := environment(config, nil); err != nil {
		return nil, err
	}
	return &Runner{config: config, process: realProcessOps()}, nil
}

func Validate(c Command) error {
	if len(c.Args) == 0 || c.Args[0] == "" || c.Dir == "" || c.Timeout <= 0 {
		return errors.New("command requires argv, directory and positive timeout")
	}
	_, err := environment(Config{}, c.Env)
	return err
}

// Run executes one command. Invalid input and a quarantined runner return errors;
// execution failures return a Result, including an explicit unset Started time.
func (r *Runner) Run(ctx context.Context, c Command) (Result, error) {
	if err := Validate(c); err != nil {
		return Result{}, err
	}
	if r.quarantined.Load() {
		return Result{}, errors.New("runner environment is quarantined after incomplete cleanup")
	}
	result := Result{ExitCode: -1, Settled: true}
	if err := ctx.Err(); err != nil {
		result.Status = Cancelled
		result.Error = err.Error()
		result.ProcessError = err
		result.Finished = time.Now()
		return result, nil //nolint:nilerr // cancellation is a terminal execution result, not invalid input.
	}
	result = r.execute(ctx, c, result)
	if !result.Settled {
		r.quarantined.Store(true)
	}
	return result, nil
}

func copyEnv(input map[string]string) map[string]string {
	out := make(map[string]string, len(input))
	maps.Copy(out, input)
	return out
}

func environment(c Config, extra map[string]string) ([]string, error) {
	env := make(map[string]string)
	for _, key := range c.Inherit {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	maps.Copy(env, c.Env)
	maps.Copy(env, extra)
	// Apply the ownership marker last, including after task and ambient values.
	env[ForegroundOwnerEnv] = "1"
	keys := make([]string, 0, len(env))
	for k, v := range env {
		if k == "" || strings.ContainsAny(k, "=\x00") || strings.ContainsRune(v, 0) {
			return nil, errors.New("invalid environment entry")
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out, nil
}

func executable(name, dir string, env []string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		if filepath.IsAbs(name) {
			return name, nil
		}
		return filepath.Abs(filepath.Join(dir, name))
	}
	path := ""
	for _, entry := range env {
		if after, ok := strings.CutPrefix(entry, "PATH="); ok {
			path = after
		}
	}
	for _, part := range filepath.SplitList(path) {
		if !filepath.IsAbs(part) {
			continue
		}
		p := filepath.Join(part, name)
		info, err := os.Stat(p)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("executable %q not found in controlled absolute PATH", name)
}

// capture is concurrency-safe because stdout/stderr copy routines can overlap.
type capture struct {
	mu    sync.Mutex
	data  []byte
	total int64
	limit int
}

func newCapture(limit int) *capture { return &capture{limit: limit} }
func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := len(p)
	c.total += int64(n)
	left := c.limit - len(c.data)
	if len(p) > left {
		p = p[:left]
	}
	c.data = append(c.data, p...)
	return n, nil
}

func (c *capture) evidence() Evidence {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Evidence{Output: string(c.data), Truncated: c.total > int64(len(c.data)), Bytes: c.total}
}

// RepositoryEnv inherits ordinary user variables, excluding Git overrides that
// could redirect repository commands away from the checked worktree.
func RepositoryEnv() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if ok && k != "" && !strings.HasPrefix(k, "GIT_") {
			env[k] = v
		}
	}
	return env
}
