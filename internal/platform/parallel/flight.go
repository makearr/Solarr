package parallel

import "sync"

// Flight shares one in-flight call among callers that ask for the same key at once: the others wait for its answer
// instead of making the call again.
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
func (f *Flight[T]) Do(key string, fn func() (T, error)) (value T, shared bool, err error) {
	f.mutex.Lock()
	if f.calls == nil {
		f.calls = map[string]*flightCall[T]{}
	}
	if running, ok := f.calls[key]; ok {
		f.mutex.Unlock()
		<-running.done
		return running.value, true, running.err
	}
	call := &flightCall[T]{done: make(chan struct{})}
	f.calls[key] = call
	f.mutex.Unlock()

	defer func() {
		f.mutex.Lock()
		delete(f.calls, key)
		f.mutex.Unlock()
		close(call.done)
	}()
	call.value, call.err = fn()
	return call.value, false, call.err
}
