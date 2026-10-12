// Package execrunner implements platform.CommandRunner with os/exec.
package execrunner

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"

	"ez-am/internal/platform"
)

// maxStreamBytes bounds each captured stream so a chatty command cannot grow
// the process without limit.
const maxStreamBytes = 64 * 1024

// Runner executes real processes. It reports a non-nil error only when the
// command could not be started or did not complete; a non-zero exit status is
// returned through CommandResult.Code with a nil error.
type Runner struct{}

func New() Runner { return Runner{} }

func (Runner) Run(ctx context.Context, req platform.CommandRequest) (platform.CommandResult, error) {
	if req.Name == "" {
		return platform.CommandResult{}, errors.New("execrunner: empty command name")
	}

	cmd := exec.CommandContext(ctx, req.Name, req.Args...)
	if len(req.Env) > 0 {
		cmd.Env = append(os.Environ(), req.Env...)
	}
	if len(req.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(req.Stdin)
	}

	stdout := newLimitedBuffer(maxStreamBytes)
	stderr := newLimitedBuffer(maxStreamBytes)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err := cmd.Run()
	result := platform.CommandResult{
		Stdout: stdout.Bytes(),
		Stderr: stderr.Bytes(),
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.Code = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}
	return result, nil
}

type limitedBuffer struct {
	mutex   sync.Mutex
	buffer  bytes.Buffer
	limit   int
	dropped bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	remaining := b.limit - b.buffer.Len()
	if remaining <= 0 {
		b.dropped = true
		return len(p), nil
	}
	if len(p) > remaining {
		b.dropped = true
		_, _ = b.buffer.Write(p[:remaining])
		return len(p), nil
	}
	return b.buffer.Write(p)
}

func (b *limitedBuffer) Bytes() []byte {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	out := make([]byte, b.buffer.Len())
	copy(out, b.buffer.Bytes())
	if b.dropped {
		out = append(out, []byte("\n[output truncated]\n")...)
	}
	return out
}
