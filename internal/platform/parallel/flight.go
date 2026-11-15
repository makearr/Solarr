package parallel

import (
	"context"
	"fmt"
	"sync"
)

// Flight shares one in-flight call among callers that ask for the same key at once: the others wait for its answer
// instead of making the call again. The call runs on its own, so no caller's cancellation ends it for the others;
// fn is given no context and must bound itself.
type Flight[T any] struct {
	mutex sync.Mutex
	calls map[string]*flightCall[T]
}

type flightCall[T any] struct {
	done  chan struct{}
	value T
	err   error
}

// Do runs fn unless a call with the same key is under way, in which case it waits for that call and answers its result.
// A caller whose ctx ends stops waiting and answers ctx's error; the call goes on for the others. A panic in fn is an
// error for everyone waiting.
func (f *Flight[T]) Do(ctx context.Context, key string, fn func() (T, error)) (value T, shared bool, err error) {
	f.mutex.Lock()
	if f.calls == nil {
		f.calls = map[string]*flightCall[T]{}
	}
	call, running := f.calls[key]
	if !running {
		call = &flightCall[T]{done: make(chan struct{})}
		f.calls[key] = call
		go f.run(key, call, fn)
	}
	f.mutex.Unlock()

	select {
	case <-call.done:
		return call.value, running, call.err
	case <-ctx.Done():
		return value, running, ctx.Err()
	}
}

func (f *Flight[T]) run(key string, call *flightCall[T], fn func() (T, error)) {
	defer func() {
		if recovered := recover(); recovered != nil {
			var zero T
			call.value, call.err = zero, fmt.Errorf("parallel: flight %q panicked: %v", key, recovered)
		}
		f.mutex.Lock()
		delete(f.calls, key)
		f.mutex.Unlock()
		close(call.done)
	}()
	call.value, call.err = fn()
}
