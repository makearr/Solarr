package iogate

import (
	"context"
	"sort"
)

// Turns takes a batch's turn on the disks under some paths; kind names the batch when it differs from its job's type.
type Turns interface {
	AcquirePaths(ctx context.Context, kind string, paths []string) (release func(), err error)
}

type turnsKey struct{}

// WithTurns carries a job's turns in ctx, for the code deep inside it that touches a disk.
func WithTurns(ctx context.Context, turns Turns) context.Context {
	return context.WithValue(ctx, turnsKey{}, turns)
}

// OnDisk runs fn while holding a turn on the disks under paths, so one batch of a long job waits its turn without the
// job holding a disk for hours. fn must not call OnDisk again: two nested turns on one disk could wait on themselves.
// Without turns in ctx (a test, work outside a job) fn simply runs.
func OnDisk(ctx context.Context, kind string, paths []string, fn func() error) error {
	turns, gated := ctx.Value(turnsKey{}).(Turns)
	sorted := make([]string, 0, len(paths))
	for _, path := range paths {
		if path != "" {
			sorted = append(sorted, path)
		}
	}
	if !gated || len(sorted) == 0 {
		return fn()
	}
	sort.Strings(sorted)
	release, err := turns.AcquirePaths(ctx, kind, sorted)
	if err != nil {
		return err
	}
	defer release()
	return fn()
}
