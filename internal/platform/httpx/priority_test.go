package httpx

import (
	"context"
	"testing"
)

func TestPriorityFrom_AnUnmarkedRequestIsBackground(t *testing.T) {
	if got := PriorityFrom(context.Background()); got != PriorityBackground {
		t.Errorf("PriorityFrom = %d, want %d", got, PriorityBackground)
	}
	if got := PriorityFrom(WithPriority(context.Background(), PriorityUser)); got != PriorityUser {
		t.Errorf("PriorityFrom = %d, want %d", got, PriorityUser)
	}
}
