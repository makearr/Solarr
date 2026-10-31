package hitcount_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/platform/hitcount"
)

func TestCounter_CountsAndAnEmptyOneIsHarmless(t *testing.T) {
	var counter hitcount.Counter
	counter.Hit()
	counter.Hit()
	counter.Miss()
	if hits, misses := counter.Totals(); hits != 2 || misses != 1 {
		t.Errorf("totals = %d, %d; want 2, 1", hits, misses)
	}
	var none *hitcount.Counter
	none.Hit()
	none.Miss()
	if hits, misses := none.Totals(); hits != 0 || misses != 0 {
		t.Errorf("nil totals = %d, %d; want 0, 0", hits, misses)
	}
}
