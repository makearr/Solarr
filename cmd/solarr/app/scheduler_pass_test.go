package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/media/tv"
)

// One task starts a metadata pass in every type that has it: duplicate only when all were on their way, errors joined.
func TestPassEnqueue_StartsThePassInEveryTypeThatHasIt(t *testing.T) {
	var started []string
	failure := errors.New("queue full")
	start := func(name string, duplicate bool, err error) media.PassStart {
		return func(_ context.Context, manual bool) (bool, error) {
			started = append(started, fmt.Sprintf("%s manual=%v", name, manual))
			return duplicate, err
		}
	}
	registry, err := media.NewRegistry(
		music.Module{Passes: media.Passes{WorksPass: start("music works", true, nil), PeoplePass: start("music people", false, failure)}},
		tv.Module{Passes: media.Passes{WorksPass: start("tv works", false, nil), PicturesPass: start("tv pictures", true, nil)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	running := &process{media: registry, logger: slog.New(slog.DiscardHandler)}

	result, err := running.passEnqueue(media.PassWorks, true)(t.Context())
	if err != nil || result.Duplicate {
		t.Errorf("works pass: duplicate %v, error %v; want a fresh start (one of two was new)", result.Duplicate, err)
	}
	if want := []string{"music works manual=true", "tv works manual=true"}; !slices.Equal(started, want) {
		t.Errorf("started %v, want %v", started, want)
	}

	started = nil
	if result, err := running.passEnqueue(media.PassPictures, false)(t.Context()); err != nil || !result.Duplicate {
		t.Errorf("pictures pass: duplicate %v, error %v; want a duplicate (the one type that has it was on its way)", result.Duplicate, err)
	}
	if _, err := running.passEnqueue(media.PassPeople, false)(t.Context()); !errors.Is(err, failure) {
		t.Errorf("people pass error = %v, want the type's own", err)
	}
	if want := []string{"tv pictures manual=false", "music people manual=false"}; !slices.Equal(started, want) {
		t.Errorf("started %v, want %v", started, want)
	}
}
