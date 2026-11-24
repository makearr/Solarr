package app

import "testing"

func TestSizeText_ReadsLikeTheFileLists(t *testing.T) {
	for bytes, want := range map[int64]string{0: "0 B", 999: "999 B", 5945986: "5.9 MB", 12_400_000: "12 MB", 1_500_000_000: "1.5 GB"} {
		if got := sizeText(bytes); got != want {
			t.Errorf("sizeText(%d) = %q, want %q", bytes, got, want)
		}
	}
}
