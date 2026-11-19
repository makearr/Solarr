package artwork

import (
	"context"
	"errors"
	"image"
	"testing"
)

func TestProcessImageWaitingForASlotReturnsWhenItsContextEnds(t *testing.T) {
	for range cap(slots) {
		slots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(slots) {
			<-slots
		}
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := ProcessImage(ctx, image.NewRGBA(image.Rect(0, 0, 10, 10))); !errors.Is(err, context.Canceled) {
		t.Errorf("ProcessImage with every slot taken = %v, want context.Canceled", err)
	}
}
