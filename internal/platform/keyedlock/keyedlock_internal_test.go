package keyedlock

import (
	"sync"
	"testing"
)

// A key is forgotten once everyone who held or awaited it has released, so a scan of many files leaves nothing behind (RACE-2).
func TestLocksForgetAKeyOnceItIsReleased(t *testing.T) {
	var locks Locks
	var group sync.WaitGroup
	for range 20 {
		group.Go(func() {
			locks.Lock("same")()
			locks.Lock("other")()
		})
	}
	group.Wait()
	if len(locks.locks) != 0 {
		t.Errorf("%d keys remembered after every release, want 0", len(locks.locks))
	}
	release := locks.Lock("held")
	if len(locks.locks) != 1 {
		t.Errorf("%d keys while one is held, want 1", len(locks.locks))
	}
	release()
}
