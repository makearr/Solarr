package keyedlock_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/makearr/Solarr/internal/platform/keyedlock"
)

func TestOneKeyRunsOneAtATimeAndKeysDoNotWaitForEachOther(t *testing.T) {
	var locks keyedlock.Locks
	var inside, worst atomic.Int32
	var group sync.WaitGroup
	for range 20 {
		group.Add(1)
		go func() {
			defer group.Done()
			release := locks.Lock("same")
			now := inside.Add(1)
			for {
				seen := worst.Load()
				if now <= seen || worst.CompareAndSwap(seen, now) {
					break
				}
			}
			inside.Add(-1)
			release()
		}()
	}
	group.Wait()
	if worst.Load() != 1 {
		t.Errorf("%d held one key at once, want 1", worst.Load())
	}
	releaseA := locks.Lock("a")
	done := make(chan struct{})
	go func() { locks.Lock("b")(); close(done) }()
	<-done
	releaseA()
}
