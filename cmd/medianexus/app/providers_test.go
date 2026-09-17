package app

import "testing"

// Every provider in the process must share one MusicBrainz budget (ADR-13):
// separate ones let concurrent jobs exceed 1 req/s and draw 503s.
func TestMusicBrainzBudget_IsOnePerProcess(t *testing.T) {
	p := &process{}

	first, second := p.musicBrainzBudget(), p.musicBrainzBudget()
	if first.limiter == nil || first.breaker == nil {
		t.Fatalf("budget = %+v, want a limiter and a breaker", first)
	}
	if first.limiter != second.limiter || first.breaker != second.breaker {
		t.Error("two calls built two budgets, want the same limiter and breaker every time")
	}
}
