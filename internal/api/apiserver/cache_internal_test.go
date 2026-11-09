package apiserver

import "testing"

func TestSameCeilingIgnoresTheKilobytesIntegerDivisionLoses(t *testing.T) {
	for _, row := range []struct {
		running, configured int64
		want                bool
	}{{393208, 393216, true}, {393216, 393216, true}, {196608, 393216, false}, {786432, 393216, false}} {
		if got := sameCeiling(row.running, row.configured); got != row.want {
			t.Errorf("sameCeiling(%d, %d) = %v, want %v", row.running, row.configured, got, row.want)
		}
	}
}
