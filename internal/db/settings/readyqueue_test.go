package settings

import "testing"

func TestReadyQueueLimitDefaultsAndRejectsOutOfRange(t *testing.T) {
	if got := (&Snapshot{policy: map[string]string{}}).ReadyQueueLimit(); got != DefaultReadyQueueLimit {
		t.Fatalf("empty snapshot: limit %d, want %d", got, DefaultReadyQueueLimit)
	}
	for stored, want := range map[string]int64{"50": 50, "0": DefaultReadyQueueLimit, "10001": DefaultReadyQueueLimit, "x": DefaultReadyQueueLimit} {
		if got := (&Snapshot{policy: map[string]string{KeyReadyQueueLimit: stored}}).ReadyQueueLimit(); got != want {
			t.Errorf("stored %q: limit %d, want %d", stored, got, want)
		}
	}
}
