package app

import (
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

// retryModule is a media type declaring only which schedule retries its waiting files.
type retryModule struct {
	key   media.Key
	retry string
}

func (m retryModule) Descriptor() media.Descriptor {
	return media.Descriptor{Key: m.key, UnidentifiedRetry: m.retry}
}

func TestSharedRetry_FollowsTheTypesDeclarationNotItsName(t *testing.T) {
	registry, err := media.NewRegistry(
		retryModule{key: "shared", retry: media.IdentifyRetryKey},
		retryModule{key: "own", retry: "own_retry"},
		retryModule{key: "none"},
	)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	got := map[string]bool{}
	for _, mediaType := range []string{"shared", "own", "none", "unregistered"} {
		got[mediaType] = sharedRetry(registry, mediaType)
	}
	want := map[string]bool{"shared": true, "own": false, "none": false, "unregistered": false}
	for mediaType, expected := range want {
		if got[mediaType] != expected {
			t.Errorf("sharedRetry(%q) = %v, want %v", mediaType, got[mediaType], expected)
		}
	}
}
