//go:build !linux

// Package fswatch reports changes inside watched directories through inotify; elsewhere it reports that it cannot.
package fswatch

import "errors"

// ErrLimit means the host's inotify watch limit is used up.
var ErrLimit = errors.New("fswatch: the host's inotify watch limit is reached")

// ErrUnsupported is what New answers away from Linux.
var ErrUnsupported = errors.New("fswatch: inotify needs Linux")

// Event is one change under a watched directory.
type Event struct {
	Dir      string
	Name     string
	IsDir    bool
	Created  bool
	Removed  bool
	Overflow bool
}

// Watcher is never built here.
type Watcher struct{}

// New always fails.
func New() (*Watcher, error) { return nil, ErrUnsupported }

// Events is never delivered.
func (*Watcher) Events() <-chan Event { return nil }

// Add always fails.
func (*Watcher) Add(string) error { return ErrUnsupported }

// Remove does nothing.
func (*Watcher) Remove(string) {}

// Count is zero.
func (*Watcher) Count() int { return 0 }

// Close does nothing.
func (*Watcher) Close() error { return nil }
