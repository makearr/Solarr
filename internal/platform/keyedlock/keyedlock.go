// Package keyedlock hands out one lock per key, so work on the same thing (a series folder, a movie) is done one at a
// time while work on different things goes side by side.
package keyedlock

import "sync"

// Locks hands out one lock per key. The zero value is ready to use.
type Locks struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

// Lock takes the key's lock and answers how to release it.
func (l *Locks) Lock(key string) func() {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[string]*sync.Mutex{}
	}
	lock, found := l.locks[key]
	if !found {
		lock = &sync.Mutex{}
		l.locks[key] = lock
	}
	l.mu.Unlock()
	lock.Lock()
	return lock.Unlock
}
