package app

import (
	"slices"
	"testing"
	"time"

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

// A provider coming back retries its own libraries' files, and flapping does not re-read them every time.
func TestTakeRecoveredMediaTypes(t *testing.T) {
	p := &process{}
	at := time.Unix(1_000_000, 0)
	p.noteProviderRecovered("images")
	if got := p.takeRecoveredMediaTypes(at); len(got) != 0 {
		t.Errorf("a non-metadata client gave %v", got)
	}
	p.noteProviderRecovered("stashdb")
	if got := p.takeRecoveredMediaTypes(at); !slices.Equal(got, []string{"adult"}) {
		t.Errorf("got %v, want adult", got)
	}
	p.noteProviderRecovered("stashdb")
	if got := p.takeRecoveredMediaTypes(at.Add(time.Minute)); len(got) != 0 {
		t.Errorf("flapping gave %v inside the spacing", got)
	}
	if got := p.takeRecoveredMediaTypes(at.Add(outageRetrySpacing + time.Minute)); !slices.Equal(got, []string{"adult"}) {
		t.Errorf("got %v, want the held note honoured after the spacing", got)
	}
}
