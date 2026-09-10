package mediatool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
)

// Runner executes one external command and returns its standard output. It is the
// seam that lets these tests run on a machine without ffprobe, the same reason
// clock.Clock exists (ADR-17).
type Runner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	// Lookup reports whether a tool is on PATH, without running it.
	Lookup(name string) error
}

// ExecRunner runs the real thing.
type ExecRunner struct{}

// Run executes name and returns its standard output. Standard error is folded into
// the error, because a tool that fails silently is the harder failure to diagnose.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr

	if err := command.Run(); err != nil {
		if message := bytes.TrimSpace(stderr.Bytes()); len(message) > 0 {
			return stdout.Bytes(), fmt.Errorf("mediatool: %s: %w: %s", name, err, message)
		}
		return stdout.Bytes(), fmt.Errorf("mediatool: %s: %w", name, err)
	}
	return stdout.Bytes(), nil
}

// Lookup reports a missing tool as ErrToolMissing, so a caller can degrade rather
// than treating it as a failed probe (ADR-30).
func (ExecRunner) Lookup(name string) error {
	if _, err := exec.LookPath(name); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: %s", ErrToolMissing, name)
		}
		return fmt.Errorf("mediatool: look up %s: %w", name, err)
	}
	return nil
}
