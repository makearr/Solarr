package music_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
)

// fakeAlbumLookup answers from fixed maps and records what was asked.
type fakeAlbumLookup struct {
	searches   map[string][]musicbrainz.ReleaseGroupMatch
	recordings map[string][]musicbrainz.RecordingMatch
	groups     map[string]musicbrainz.ReleaseGroup
	releases   map[string][]musicbrainz.Release
	err        error
	asked      []string
}

func (f *fakeAlbumLookup) Search(_ context.Context, _ musicbrainz.Priority, artist, title string, _ int) ([]musicbrainz.ReleaseGroupMatch, error) {
	f.asked = append(f.asked, "search:"+artist+"/"+title)
	return f.searches[artist+"/"+title], f.err
}

func (f *fakeAlbumLookup) SearchRecordings(_ context.Context, _ musicbrainz.Priority, artist, title string, _ int) ([]musicbrainz.RecordingMatch, error) {
	f.asked = append(f.asked, "recording:"+artist+"/"+title)
	return f.recordings[title], f.err
}

func (f *fakeAlbumLookup) Album(_ context.Context, _ musicbrainz.Priority, rg, _ string) (musicbrainz.ReleaseGroup, musicbrainz.Release, error) {
	f.asked = append(f.asked, "album:"+rg)
	return f.groups[rg], musicbrainz.Release{}, f.err
}

func (f *fakeAlbumLookup) Releases(_ context.Context, _ musicbrainz.Priority, rg string) ([]musicbrainz.Release, error) {
	f.asked = append(f.asked, "releases:"+rg)
	return f.releases[rg], f.err
}

var studioRelease = releaseOf("rel-studio", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks)

func TestIdentifyAlbum_FromTheAlbumTags(t *testing.T) {
	lookup := &fakeAlbumLookup{
		searches: map[string][]musicbrainz.ReleaseGroupMatch{"P!po/Kebilegev Bibihi": {
			{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-studio", Title: "Kebilegev Bibihi", PrimaryType: "Album"}, ArtistCredit: "P!po"},
			{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-remixes", Title: "Kebilegev Bibihi (The Remixes)", PrimaryType: "EP"}, ArtistCredit: "P!po"},
		}},
		releases: map[string][]musicbrainz.Release{"rg-studio": {studioRelease}},
	}

	decision, err := music.IdentifyAlbum(t.Context(), lookup, musicbrainz.PriorityIdentify, folderOf(traumaTracks), nil)
	if err != nil {
		t.Fatalf("IdentifyAlbum: %v", err)
	}
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-studio" ||
		decision.Best.Candidate.Recall != music.RecallAlbumTags {
		t.Fatalf("decision = %s %q, want identified rg-studio from album tags", decision.Outcome, bestReleaseGroup(decision))
	}
	if slices.ContainsFunc(lookup.asked, func(asked string) bool { return strings.HasPrefix(asked, "recording:") }) {
		t.Errorf("asked %v, want no recording search once the album tags recalled two groups", lookup.asked)
	}
}

// With no album tag, the track titles find the album the tracks share, studio before compilation.
func TestIdentifyAlbum_RecordingSearchWhenTheAlbumTagIsMissing(t *testing.T) {
	locals := folderOf(traumaTracks)
	for i := range locals {
		locals[i].Album = music.Claim{}
	}
	compilation := musicbrainz.ReleaseGroup{MBID: "rg-now", Title: "Now 98", PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}}
	studio := musicbrainz.ReleaseGroup{MBID: "rg-studio", Title: "Kebilegev Bibihi", PrimaryType: "Album"}
	recordings := map[string][]musicbrainz.RecordingMatch{}
	for _, track := range traumaTracks {
		recordings[track.title] = []musicbrainz.RecordingMatch{{Title: track.title, ReleaseGroups: []musicbrainz.ReleaseGroup{compilation, studio}}}
	}
	lookup := &fakeAlbumLookup{
		recordings: recordings,
		groups:     map[string]musicbrainz.ReleaseGroup{"rg-studio": {MBID: "rg-studio", Title: "Kebilegev Bibihi", PrimaryType: "Album", ArtistCredit: []musicbrainz.ArtistCredit{{Name: "P!po"}}}},
		releases:   map[string][]musicbrainz.Release{"rg-studio": {studioRelease}},
	}

	decision, err := music.IdentifyAlbum(t.Context(), lookup, musicbrainz.PriorityIdentify, locals, nil)
	if err != nil {
		t.Fatalf("IdentifyAlbum: %v", err)
	}
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-studio" {
		t.Fatalf("decision = %s %q (asked %v), want identified rg-studio", decision.Outcome, bestReleaseGroup(decision), lookup.asked)
	}
	if want := "album:rg-studio"; !slices.Contains(lookup.asked, want) {
		t.Errorf("asked %v, want %q: recording search has no credit, so the group must be looked up", lookup.asked, want)
	}
}

// A seed from identified siblings is scored even when no search would find it.
func TestIdentifyAlbum_ScoresTheSiblingSeed(t *testing.T) {
	lookup := &fakeAlbumLookup{
		groups:   map[string]musicbrainz.ReleaseGroup{"rg-studio": {MBID: "rg-studio", Title: "Kebilegev Bibihi", PrimaryType: "Album", ArtistCredit: []musicbrainz.ArtistCredit{{Name: "P!po"}}}},
		releases: map[string][]musicbrainz.Release{"rg-studio": {studioRelease}},
	}

	decision, err := music.IdentifyAlbum(t.Context(), lookup, musicbrainz.PriorityIdentify, folderOf(traumaTracks), []string{"rg-studio"})
	if err != nil {
		t.Fatalf("IdentifyAlbum: %v", err)
	}
	if decision.Outcome != music.AlbumIdentified || decision.Best.Candidate.Recall != music.RecallSibling {
		t.Errorf("decision = %s %+v, want identified from the sibling seed", decision.Outcome, decision.Best)
	}
}

// Every lookup failing says nothing about the folder, so it must not read as "no candidate".
func TestIdentifyAlbum_EveryLookupFailingIsAnError(t *testing.T) {
	lookup := &fakeAlbumLookup{err: errors.New("provider down")}
	if _, err := music.IdentifyAlbum(t.Context(), lookup, musicbrainz.PriorityIdentify, folderOf(traumaTracks), nil); err == nil {
		t.Fatal("IdentifyAlbum succeeded with every lookup failing")
	}
}
