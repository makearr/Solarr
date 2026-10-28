package music_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/clock"
)

type fakeLabelLogos struct{ asked [][]string }

func (f *fakeLabelLogos) LabelLogos(_ context.Context, mbids []string) (map[string]string, error) {
	f.asked = append(f.asked, mbids)
	return map[string]string{"label-a": "https://commons.wikimedia.org/wiki/Special:FilePath/A.svg?width=500"}, nil
}

func TestEachLabelsLogoIsAskedOnceAndKeptWhenWikidataHasOne(t *testing.T) {
	opened := openKnownAlbumsLibrary(t)
	organisations := library.NewOrganisations(opened)
	works, err := library.NewIdentity(opened).ResolveWorks(t.Context(), clock.NewFakeAtEpoch().Now(), []library.WorkLink{
		{Provider: music.ProviderReleaseGroup, ExternalID: "rg-a", Confidence: 1, Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "A", SortTitle: "a"}},
		{Provider: music.ProviderReleaseGroup, ExternalID: "rg-b", Confidence: 1, Work: library.WorkIdentity{MediaType: "music", CanonicalTitle: "B", SortTitle: "b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	labelA, _, err := organisations.SetWorkOrganisation(t.Context(), clock.NewFakeAtEpoch().Now(), works[0], music.RoleLabel, "musicbrainz",
		&library.OrganisationLink{Kind: music.RoleLabel, Name: "Label A", Provider: music.ProviderLabel, ExternalID: "label-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := organisations.SetWorkOrganisation(t.Context(), clock.NewFakeAtEpoch().Now(), works[1], music.RoleLabel, "musicbrainz",
		&library.OrganisationLink{Kind: music.RoleLabel, Name: "Label B", Provider: music.ProviderLabel, ExternalID: "label-b"}); err != nil {
		t.Fatal(err)
	}
	source := &fakeLabelLogos{}
	job := music.LabelLogosJob{Organisations: organisations, Source: source, Clock: clock.NewFakeAtEpoch(), Logger: slog.New(slog.DiscardHandler)}
	for range 2 {
		if err := job.Run(t.Context(), jobs.Job{}); err != nil {
			t.Fatal(err)
		}
	}
	if len(source.asked) != 1 || len(source.asked[0]) != 2 {
		t.Errorf("asked = %v, want both labels in one query, and nothing on the second run", source.asked)
	}
	profile, err := organisations.Get(t.Context(), labelA, nil)
	if err != nil {
		t.Fatal(err)
	}
	if profile.LogoURL != "https://commons.wikimedia.org/wiki/Special:FilePath/A.svg?width=500" {
		t.Errorf("logo = %q, want Wikidata's", profile.LogoURL)
	}
}
