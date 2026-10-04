// Package parallel runs a bounded number of calls at once; a panic in one becomes that call's error.
package parallel

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
)

// PanicError is a panic recovered from one call, with the stack it happened on.
type PanicError struct {
	Value any
	Stack []byte
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// Do calls fn for every index in [0, n), at most width at once, and waits for all of them. It answers each call's
// error at its index. A width of zero or less runs every call at once. A panic in fn never reaches the process: a
// goroutine a job starts is outside the executor's own recovery, so it is recovered here.
func Do(ctx context.Context, n, width int, fn func(ctx context.Context, index int) error) []error {
	errs := make([]error, n)
	if n == 0 {
		return errs
	}
	if width <= 0 || width > n {
		width = n
	}
	slots := make(chan struct{}, width)
	var running sync.WaitGroup
	for index := range n {
		slots <- struct{}{}
		running.Go(func() {
			defer func() { <-slots }()
			errs[index] = call(ctx, index, fn)
		})
	}
	running.Wait()
	return errs
}

func call(ctx context.Context, index int, fn func(ctx context.Context, index int) error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = &PanicError{Value: recovered, Stack: debug.Stack()}
		}
	}()
	return fn(ctx, index)
}
