// Package keyedlock hands out one lock per key, so work on the same thing (a series folder, a movie) is done one at a
// time while work on different things goes side by side.
package keyedlock

import "sync"

// Locks hands out one lock per key. The zero value is ready to use. A key is remembered only while someone holds or awaits it.
type Locks struct {
	mu    sync.Mutex
	locks map[string]*keyLock
}

type keyLock struct {
	mu      sync.Mutex
	holders int
}

// Lock takes the key's lock and answers how to release it.
func (l *Locks) Lock(key string) func() {
	if l == nil {
		return func() {}
	}
	l.mu.Lock()
	if l.locks == nil {
		l.locks = map[string]*keyLock{}
	}
	lock, found := l.locks[key]
	if !found {
		lock = &keyLock{}
		l.locks[key] = lock
	}
	lock.holders++
	l.mu.Unlock()
	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		l.mu.Lock()
		lock.holders--
		if lock.holders == 0 {
			delete(l.locks, key)
		}
		l.mu.Unlock()
	}
}
