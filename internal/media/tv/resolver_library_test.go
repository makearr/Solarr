package tv_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/metadata/skyhook"
)

// noSearchSkyhook knows a series by id and cannot search, so only a series the library holds can be placed.
type noSearchSkyhook struct{ series skyhook.Series }

func (s noSearchSkyhook) Series(context.Context, int64) (skyhook.Series, error) { return s.series, nil }
func (s noSearchSkyhook) Search(context.Context, string) ([]skyhook.Series, error) {
	return nil, errors.New("no search")
}

func TestAFolderYearThatTheTVDBQualifiesInTheTitleFindsTheHeldSeries(t *testing.T) {
	sky := fixtureSkyhook()
	sky.Title, sky.FirstAired = "Example Show (2003)", "2005-01-14"
	service, opened := newService(t, noSearchSkyhook{series: sky}, fakeTVmaze{err: errors.New("down")})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorNone})
	if err != nil {
		t.Fatal(err)
	}
	resolver := tv.Resolver{Service: service, Reader: library.NewSeriesReader(opened), Clock: service.Clock, DefaultProfileID: 1, Categories: categories{}}
	got, _ := resolver.ResolveFolder(t.Context(), 3, "Example Show (2003)", []string{"Example Show (2003)/Specials/x - S00E01.mp4"}, tv.FolderGuess{})
	if got != added.WorkID {
		t.Errorf("resolved %d, want the held series %d without asking the providers again", got, added.WorkID)
	}
}

func TestADirectoryOfSpecialsIsPlacedOnTheSeriesItsFolderWasPlacedOn(t *testing.T) {
	first, opened := newService(t, searchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: errors.New("down")})
	memory := &tv.FolderMemory{}
	resolver := tv.Resolver{Service: first, Reader: library.NewSeriesReader(opened), Clock: first.Clock, DefaultProfileID: 1, Categories: categories{}, Resolved: memory}
	placed, _ := resolver.ResolveFolder(t.Context(), 3, "Example Show", []string{"Example Show/Season 1/Example Show - S01E01.mp4"}, tv.FolderGuess{})
	if placed == 0 {
		t.Fatal("the folder was not placed")
	}
	elsewhere, other := newService(t, noSearchSkyhook{series: fixtureSkyhook()}, fakeTVmaze{err: errors.New("down")})
	later := tv.Resolver{Service: elsewhere, Reader: library.NewSeriesReader(other), Clock: first.Clock, DefaultProfileID: 1, Categories: categories{}, Resolved: memory}
	got, _ := later.ResolveFolder(t.Context(), 3, "Example Show", []string{"Example Show/Specials/Example Show - S00E01.mp4"}, tv.FolderGuess{})
	if got != placed {
		t.Errorf("specials resolved to %d, want the folder's series %d", got, placed)
	}
}

func TestAFolderNamedLikeAnAliasOfAHeldSeriesIsNotPlacedOnIt(t *testing.T) {
	sky := fixtureSkyhook()
	service, opened := newService(t, noSearchSkyhook{series: sky}, fakeTVmaze{err: errors.New("down")})
	added, err := service.Add(t.Context(), tv.AddRequest{IDs: tv.IDs{TVDB: 1234}, CategoryID: 3, ProfileID: 1, Monitor: tv.MonitorNone})
	if err != nil {
		t.Fatal(err)
	}
	write := tv.ToWrite(added.Series, tv.WriteOptions{Now: addNow})
	write.Aliases = []string{"Example Show Classic"}
	write.Parts = nil
	if _, err := service.Series.Upsert(t.Context(), addNow, write); err != nil {
		t.Fatal(err)
	}
	resolver := tv.Resolver{Service: service, Reader: library.NewSeriesReader(opened), Clock: service.Clock, Logger: slog.Default(), DefaultProfileID: 1, Categories: categories{}}
	if got, _ := resolver.ResolveFolder(t.Context(), 3, "Example Show Classic", []string{"Example Show Classic/Season 1/x - S01E01.mp4"}, tv.FolderGuess{}); got != 0 {
		t.Errorf("the alias placed the folder on series %d", got)
	}
}
