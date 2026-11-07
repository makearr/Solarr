package settings_test

import "testing"

func TestVideoPreviewsAreOffUnlessTurnedOnAndTurnThePerceptualHashOnWithThem(t *testing.T) {
	cache, clock := newCache(t)
	if cache.Snapshot().VideoPreviews() {
		t.Error("previews are on by default")
	}
	if err := cache.SetVideoPreviews(t.Context(), clock, true); err != nil {
		t.Fatal(err)
	}
	snapshot := cache.Snapshot()
	if !snapshot.VideoPreviews() || !snapshot.VideoPerceptualHash() {
		t.Errorf("previews %v, hashing %v; want both on", snapshot.VideoPreviews(), snapshot.VideoPerceptualHash())
	}
	if err := cache.SetVideoPreviews(t.Context(), clock, false); err != nil {
		t.Fatal(err)
	}
	snapshot = cache.Snapshot()
	if snapshot.VideoPreviews() || !snapshot.VideoPerceptualHash() {
		t.Errorf("previews %v, hashing %v; want previews off and hashing left on", snapshot.VideoPreviews(), snapshot.VideoPerceptualHash())
	}
}
