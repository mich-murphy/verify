//go:build darwin || linux

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// processOps is an instance-local OS boundary. New always installs real operations;
// tests may replace an instance before use, never shared package-global functions.
type processOps struct {
	signal func(int, syscall.Signal) error
	table  func(context.Context) ([]byte, error)
}

func realProcessOps() processOps {
	return processOps{signal: signalGroup, table: processTable}
}

func processTable(ctx context.Context) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/bin/ps", "-axo", "pgid=,stat=")
	cmd.Env = []string{"LC_ALL=C"}
	return cmd.Output()
}

func (r *Runner) groupAlive(ctx context.Context, group int) (bool, error) {
	return inspectGroup(ctx, group, r.process.table)
}

func (r *Runner) execute(parent context.Context, t Command, result Result) Result {
	ctx, cancel := context.WithTimeout(parent, t.Timeout)
	defer cancel()
	env, err := environment(r.config, t.Env)
	if err != nil {
		return executionError(result, err)
	}
	path, err := executable(t.Args[0], t.Dir, env)
	if err != nil {
		return executionError(result, err)
	}
	// #nosec G204 -- Intentionally executes validated argv with controlled PATH/env; never an implicit shell.
	// Detach exec's direct-child cancellation: below we terminate the entire owned group before reaping.
	cmd := exec.CommandContext(context.WithoutCancel(ctx), path, t.Args[1:]...)
	cmd.Dir = t.Dir
	cmd.Stdin = bytes.NewReader(t.Stdin)
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bound inherited output pipe lifetimes when a parent exits before its child.
	cmd.WaitDelay = 50 * time.Millisecond
	stdout, stderr := newCapture(r.config.OutputLimit), newCapture(r.config.OutputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		return executionError(result, err)
	}
	result.Started = time.Now()
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	reaped := true
	select {
	case err = <-waited:
		result.Status = Pass
		if err != nil {
			result.Status = Fail
			result.Error = err.Error()
		}
	case <-ctx.Done():
		result.Status = Cancelled
		if parent.Err() == nil {
			result.Status = TimedOut
		}
		result.Error = ctx.Err().Error()
		var stopErr error
		reaped, stopErr = r.stopAndWait(ctx, cmd.Process.Pid, waited)
		if stopErr != nil {
			result.Error += "; " + stopErr.Error()
		}
	}
	if reaped {
		result.ExitCode = cmd.ProcessState.ExitCode()
	} else {
		result.Settled = false
		result.Error += "; quarantine: direct child not reaped"
	}
	// Cleanup must outlive task cancellation but is itself strictly bounded.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), r.config.SettleTimeout+time.Second)
	defer cleanupCancel()
	alive, checkErr := r.groupAlive(cleanupCtx, cmd.Process.Pid)
	if alive || checkErr != nil {
		if result.Status == Pass {
			result.Status = Fail
			result.Error = "command exited before owned descendants settled"
		}
		cleanupErr := r.settle(cleanupCtx, cmd.Process.Pid)
		if checkErr != nil || cleanupErr != nil {
			result.Settled = false
			result.Error += "; quarantine: " + errors.Join(checkErr, cleanupErr).Error()
		}
	}
	result.Stdout = stdout.evidence()
	result.Stderr = stderr.evidence()
	result.Finished = time.Now()
	return result
}

func executionError(result Result, err error) Result {
	result.Status = Fail
	result.Error = err.Error()
	result.Finished = time.Now()
	return result
}

func signalGroup(pid int, signal syscall.Signal) error {
	err := syscall.Kill(-pid, signal)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// groupAlive excludes zombies: those cannot execute or retain open resources.
// Only our unique process group is signalled, never a port owner or name match.
func inspectGroup(parent context.Context, group int, table func(context.Context) ([]byte, error)) (bool, error) {
	ctx, cancel := context.WithTimeout(parent, time.Second)
	defer cancel()
	out, err := table(ctx)
	if err != nil {
		return false, fmt.Errorf("inspect owned process group: %w", err)
	}
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil {
			return false, err
		}
		if id == group && !strings.HasPrefix(fields[1], "Z") {
			return true, nil
		}
	}
	return false, nil
}

func (r *Runner) settle(ctx context.Context, group int) error {
	if err := r.process.signal(group, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(r.config.SettleTimeout)
	killAt := time.Now().Add(r.config.KillGrace)
	for {
		alive, err := r.groupAlive(ctx, group)
		if err != nil {
			return err
		}
		if !alive {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("owned descendants did not settle")
		}
		if time.Now().After(killAt) {
			if err := r.process.signal(group, syscall.SIGKILL); err != nil {
				return err
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// stopAndWait reaps the direct child after cancellation. Its wait error is not
// a separate outcome: cancellation/timeout is already the required failure.
func (r *Runner) stopAndWait(parent context.Context, pid int, waited <-chan error) (bool, error) {
	// A failed kill or uninterruptible process must quarantine, not hang release.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), r.config.SettleTimeout)
	defer cancel()
	err := r.process.signal(pid, syscall.SIGTERM)
	timer := time.NewTimer(r.config.KillGrace)
	defer timer.Stop()
	select {
	case waitErr := <-waited:
		return true, errors.Join(err, waitErr)
	case <-ctx.Done():
		return false, errors.Join(err, ctx.Err())
	case <-timer.C:
		err = errors.Join(err, r.process.signal(pid, syscall.SIGKILL))
	}
	select {
	case waitErr := <-waited:
		return true, errors.Join(err, waitErr)
	case <-ctx.Done():
		return false, errors.Join(err, ctx.Err())
	}
}
