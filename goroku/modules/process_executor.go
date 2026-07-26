package modules

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Subprocess pools.
//
// There used to be one global slot for every external process, so a
// `.terminal tail -f` (15 minute timeout) blocked `.dlmod` plugin builds and
// every eval until it finished — and the caller only saw "⏳ Compiling…" until
// its own context expired, with no signal that it was queued behind something
// unrelated. The pools are independent because the work is unrelated; each
// stays serialized within itself, which is what the original limit was for.
const (
	interactiveExecutorConcurrency = 1 // .terminal — long-lived, user-attached
	buildExecutorConcurrency       = 1 // plugin/compiler builds — CPU and disk heavy
	evalExecutorConcurrency        = 2 // python/node/php/ruby/rust eval — short
)

// ErrExecutorBusy reports that a pool's slots are all taken. Callers surface it
// to the user instead of waiting silently until their own deadline expires.
var ErrExecutorBusy = errors.New("busy")

// ProcessResult is the structured outcome of a managed external process.
type ProcessResult struct {
	ExitCode  int
	TimedOut  bool
	Canceled  bool
	Truncated bool
	Duration  time.Duration
	Stdout    []byte
	Stderr    []byte
	Err       error
}

// ProcessSpec describes a process launch under ProcessExecutor policy.
type ProcessSpec struct {
	Name          string
	Args          []string
	Dir           string // empty => process default (usually caller cwd)
	Env           []string
	ExtraEnv      []string
	InheritEnv    bool // terminal compatibility; prefer false for eval/build
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
	OutputLimit   int
	CaptureOutput bool
}

// ProcessExecutor provides deadline-aware, bounded, process-group managed exec.
type ProcessExecutor struct {
	slots        chan struct{}
	defaultLimit int
}

func NewProcessExecutor(concurrency, outputLimit int) *ProcessExecutor {
	if concurrency < 1 {
		concurrency = 1
	}
	if outputLimit <= 0 {
		outputLimit = externalOutputLimit
	}
	return &ProcessExecutor{
		slots:        make(chan struct{}, concurrency),
		defaultLimit: outputLimit,
	}
}

var (
	// interactiveExecutor runs .terminal: one at a time, and a caller that
	// finds it busy is told so rather than queued.
	interactiveExecutor = NewProcessExecutor(interactiveExecutorConcurrency, externalOutputLimit)
	// buildExecutor runs plugin builds and compilers.
	buildExecutor = NewProcessExecutor(buildExecutorConcurrency, externalOutputLimit)
	// evalExecutor runs interpreted/compiled eval languages.
	evalExecutor = NewProcessExecutor(evalExecutorConcurrency, externalOutputLimit)
)

func secureDefaultEnv(extra ...string) []string {
	path := os.Getenv("PATH")
	if path == "" {
		path = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
	}
	home := os.Getenv("HOME")
	tmp := os.TempDir()
	env := []string{
		"PATH=" + path,
		"HOME=" + home,
		"TMPDIR=" + tmp,
		"LANG=C.UTF-8",
		"LC_ALL=C.UTF-8",
	}
	if user := os.Getenv("USER"); user != "" {
		env = append(env, "USER="+user)
	}
	if term := os.Getenv("TERM"); term != "" {
		env = append(env, "TERM="+term)
	}
	return append(env, extra...)
}

func (e *ProcessExecutor) acquire(ctx context.Context) error {
	select {
	case e.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// TryAcquire takes a slot without waiting. It reports ErrExecutorBusy when the
// pool is saturated, so a command can tell the user "busy" instead of hanging
// until its own context expires.
func (e *ProcessExecutor) TryAcquire() error {
	select {
	case e.slots <- struct{}{}:
		return nil
	default:
		return ErrExecutorBusy
	}
}

func (e *ProcessExecutor) release() {
	select {
	case <-e.slots:
	default:
	}
}

func (e *ProcessExecutor) buildEnv(spec ProcessSpec) []string {
	if len(spec.Env) > 0 {
		return append(append([]string(nil), spec.Env...), spec.ExtraEnv...)
	}
	if spec.InheritEnv {
		return append(os.Environ(), spec.ExtraEnv...)
	}
	return secureDefaultEnv(spec.ExtraEnv...)
}

// Command prepares an *exec.Cmd with process-group kill and concurrency slot.
// Caller must Release after the process finishes (Wait/Run).
func (e *ProcessExecutor) Command(ctx context.Context, spec ProcessSpec) (*exec.Cmd, error) {
	return e.command(ctx, spec, true)
}

// CommandNoWait is Command that reports ErrExecutorBusy instead of queueing
// behind another process in the same pool. Use it where the user is waiting on
// a reply: silently blocking until the caller's own deadline expires reads as a
// hang, not as "something else is running".
func (e *ProcessExecutor) CommandNoWait(ctx context.Context, spec ProcessSpec) (*exec.Cmd, error) {
	return e.command(ctx, spec, false)
}

func (e *ProcessExecutor) command(ctx context.Context, spec ProcessSpec, wait bool) (*exec.Cmd, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return nil, fmt.Errorf("empty process name")
	}
	acquire := e.acquire
	if !wait {
		acquire = func(context.Context) error { return e.TryAcquire() }
	}
	if err := acquire(ctx); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, spec.Name, spec.Args...) //nolint:gosec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	if spec.Dir != "" {
		cmd.Dir = spec.Dir
	}
	cmd.Env = e.buildEnv(spec)
	if spec.Stdin != nil {
		cmd.Stdin = spec.Stdin
	}
	if spec.Stdout != nil {
		cmd.Stdout = spec.Stdout
	}
	if spec.Stderr != nil {
		cmd.Stderr = spec.Stderr
	}
	return cmd, nil
}

func (e *ProcessExecutor) Release() { e.release() }

// Run executes the process to completion with optional bounded capture.
func (e *ProcessExecutor) Run(ctx context.Context, spec ProcessSpec) ProcessResult {
	started := time.Now()
	limit := spec.OutputLimit
	if limit <= 0 {
		limit = e.defaultLimit
	}
	var stdoutBuf, stderrBuf *boundedBuffer
	if spec.CaptureOutput || (spec.Stdout == nil && spec.Stderr == nil) {
		stdoutBuf = newBoundedBuffer(limit)
		stderrBuf = newBoundedBuffer(limit)
		if spec.Stdout == nil {
			spec.Stdout = stdoutBuf
		}
		if spec.Stderr == nil {
			spec.Stderr = stderrBuf
		}
		spec.CaptureOutput = true
	}
	cmd, err := e.Command(ctx, spec)
	if err != nil {
		return ProcessResult{Err: err, Duration: time.Since(started), ExitCode: -1}
	}
	defer e.Release()

	runErr := cmd.Run()
	res := ProcessResult{
		Duration: time.Since(started),
		Err:      runErr,
	}
	if stdoutBuf != nil {
		res.Stdout = stdoutBuf.Bytes()
		res.Truncated = stdoutBuf.Truncated()
	}
	if stderrBuf != nil {
		res.Stderr = stderrBuf.Bytes()
		res.Truncated = res.Truncated || stderrBuf.Truncated()
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	} else if errors.Is(ctx.Err(), context.Canceled) {
		res.Canceled = true
	}
	if runErr == nil {
		res.ExitCode = 0
		return res
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		res.ExitCode = exitErr.ExitCode()
	} else {
		res.ExitCode = -1
	}
	return res
}

// secureCommandContext builds a managed command on the eval pool. The slot is
// held until releaseCommandSlot. Prefer ProcessExecutor for new code.
func secureCommandContext(ctx context.Context, name string, args ...string) (*exec.Cmd, error) {
	return evalExecutor.Command(ctx, ProcessSpec{
		Name: name,
		Args: args,
	})
}

func releaseCommandSlot() { evalExecutor.Release() }
