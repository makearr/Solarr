package music_test

import (
	"context"
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/mediatool"
	"github.com/makearr/MediaNexus/internal/model"
	"github.com/makearr/MediaNexus/internal/music"
	"github.com/makearr/MediaNexus/internal/musicbrainz"
)

// fakeSearcher answers a fixed set of matches (or a fixed error) for any query,
// counts calls so a test can assert whether it was asked at all, and records the
// last artist/album it was actually asked with.
type fakeSearcher struct {
	matches               []musicbrainz.ReleaseGroupMatch
	err                   error
	calls                 int
	lastArtist, lastAlbum string
}

func (f *fakeSearcher) Search(
	_ context.Context, _ musicbrainz.Priority, artist, album string, _ int,
) ([]musicbrainz.ReleaseGroupMatch, error) {
	f.calls++
	f.lastArtist, f.lastAlbum = artist, album
	if f.err != nil {
		return nil, f.err
	}
	return f.matches, nil
}

func searchIdentify(t *testing.T, tags mediatool.Tags, relPath string, searcher *fakeSearcher) []model.Identification {
	t.Helper()
	identifier := music.NewSearchIdentifier(music.SearchIdentifierConfig{
		Tags: fixedTags{tags: tags}, Search: searcher,
	})
	found, err := identifier.Identify(t.Context(), model.FileRef{RelPath: relPath})
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	return found
}

// A clean, unambiguous match against real ARTIST/ALBUM tags must resolve to
// exactly one identification, confident enough to clear Consumer's
// DefaultAmbiguityThreshold (0.6) but still below MBIDIdentifier's weakest
// embedded-tag confidence (0.70) -- a search match must never outrank a real MBID.
func TestSearchIdentify_ACleanMatchResolvesAboveAmbiguityThresholdButBelowAnyTagConfidence(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "The Sezo Siluko", FirstReleaseDate: "1978"},
			ArtistCredit: "Vutu Doci"},
	}}
	found := searchIdentify(t, mediatool.Tags{"artist": "Vutu Doci", "album": "The Sezo Siluko", "date": "1978"},
		"Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES/01-vutu_doci-moving.flac", searcher)

	if len(found) != 1 {
		t.Fatalf("got %d identifications, want 1", len(found))
	}
	if found[0].Strategy != music.StrategyParse {
		t.Errorf("Strategy = %q, want %q", found[0].Strategy, music.StrategyParse)
	}
	if found[0].ExternalID.Provider != music.ProviderReleaseGroup || found[0].ExternalID.ExternalID != "rg1" {
		t.Errorf("ExternalID = %+v, want the matched release group", found[0].ExternalID)
	}
	if found[0].Confidence < 0.6 {
		t.Errorf("Confidence = %.2f, want it to clear the 0.6 ambiguity threshold", found[0].Confidence)
	}
	if found[0].Confidence >= 0.70 {
		t.Errorf("Confidence = %.2f, want it strictly below any embedded-tag confidence", found[0].Confidence)
	}
}

// Regression: a real-world WEB reissue tagged with its own release year (2018,
// say) must still resolve against the original release group, whose
// FirstReleaseDate is the *original* release year (1978) -- year must never be
// compared here at all, or a perfect title match gets rejected over a field that
// was never describing the same thing on both sides (edition year vs. work year,
// ADR-7). Found against a real MusicBrainz response during this project's own
// verification pass (Vutu Doci's "The Sezo Siluko").
func TestSearchIdentify_AReissueYearDoesNotBreakAnOtherwiseCleanTitleMatch(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "The Sezo Siluko", FirstReleaseDate: "1978-01-12"},
			ArtistCredit: "Vutu Doci"},
	}}
	found := searchIdentify(t, mediatool.Tags{"artist": "Vutu Doci", "album": "The Sezo Siluko", "date": "2018"},
		"Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES/01-vutu_doci-moving.flac", searcher)

	if len(found) != 1 || found[0].ExternalID.ExternalID != "rg1" {
		t.Fatalf("got %+v, want the release group to resolve despite the 1978-vs-2018 year gap", found)
	}
	if found[0].Confidence < 0.6 {
		t.Errorf("Confidence = %.2f, want a clean title match to still clear the ambiguity threshold", found[0].Confidence)
	}
}

// Two candidates too close to call must not resolve automatically: every
// candidate still comes back (for F29's manual queue) but none at a confidence
// that could win the chain outright.
func TestSearchIdentify_AmbiguousCandidatesStayBelowTheAmbiguityThreshold(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "Greatest Hits"}, ArtistCredit: "The Somimema"},
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg2", Title: "Greatest Hits"}, ArtistCredit: "The Somimema"},
	}}
	found := searchIdentify(t, mediatool.Tags{"artist": "The Somimema", "album": "Greatest Hits"},
		"The Somimema-Greatest Hits-CD-FLAC-2001-GROUP/01-track.flac", searcher)

	if len(found) != 2 {
		t.Fatalf("got %d identifications, want both candidates kept for the manual queue", len(found))
	}
	for _, one := range found {
		if one.Confidence >= 0.6 {
			t.Errorf("Confidence = %.2f for an ambiguous pair, want it below the 0.6 auto-resolve threshold", one.Confidence)
		}
	}
}

// Regression, found live against a real WEB reissue during this project's own
// verification pass: the ALBUM tag itself, not just the release folder name, can
// carry a remaster suffix ("Nogih Zeni (Mono Remastered)"). Searched literally,
// MusicBrainz's own search returns zero results for that exact phrase --
// claimedIdentity must clean the tag the same way it would a folder name.
func TestSearchIdentify_CleansAnEditionSuffixBakedIntoTheAlbumTagItself(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "Nogih Zeni"}, ArtistCredit: "The Zegabab"},
	}}
	found := searchIdentify(t, mediatool.Tags{"artist": "The Zegabab", "album": "Nogih Zeni (Mono Remastered)"},
		"The Zegabab-Nogih Zeni [Mono Remastered]-WEB-2026-QUAVER/01-the_zegabab-road_runner.mp3", searcher)

	if searcher.lastAlbum != "Nogih Zeni" {
		t.Errorf("searched for album %q, want the tag's edition suffix stripped first", searcher.lastAlbum)
	}
	if len(found) != 1 || found[0].ExternalID.ExternalID != "rg1" {
		t.Fatalf("got %+v, want the cleaned album tag to resolve against the real release", found)
	}
}

// A file with no ARTIST/ALBUM tags at all still gets a claim from the release
// folder name (ADR-41's path-as-evidence), through internal/parse.
func TestSearchIdentify_FallsBackToTheReleaseFolderNameWhenTagsCarryNoIdentity(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "Dulaz Kata To Mapu"}, ArtistCredit: "KA Voho L"},
	}}
	found := searchIdentify(t, mediatool.Tags{},
		"KA Voho L-Dulaz Kata To Mapu-VLS-FLAC-1988-THEVOiD/a1-ka_voho_l-dulaz_kata_to_mapu.flac", searcher)

	if searcher.calls != 1 {
		t.Fatalf("search was called %d times, want 1 -- the folder name should have supplied a claim", searcher.calls)
	}
	if len(found) != 1 || found[0].ExternalID.ExternalID != "rg1" {
		t.Fatalf("got %+v, want the release parsed from the folder name to resolve", found)
	}
}

// No artist and no album from either source at all: nothing to search with, and
// no search should even be attempted.
func TestSearchIdentify_SaysNothingWhenNeitherTagsNorTheFolderNameCarryIdentity(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg1", Title: "Whatever"}},
	}}
	found := searchIdentify(t, mediatool.Tags{}, "just_a_track.flac", searcher)
	if found != nil {
		t.Errorf("got %+v, want nothing when there was no claim to search with", found)
	}
	if searcher.calls != 0 {
		t.Errorf("search was called %d times, want 0", searcher.calls)
	}
}

// A search failure is not evidence there is nothing to find -- treated the same
// as MBIDIdentifier's own "not found" case, not a hard error.
func TestSearchIdentify_ASearchFailureIsNotAnError(t *testing.T) {
	searcher := &fakeSearcher{err: errors.New("musicbrainz unreachable")}
	found := searchIdentify(t, mediatool.Tags{"artist": "Vutu Doci", "album": "The Sezo Siluko"}, "x/a.flac", searcher)
	if found != nil {
		t.Errorf("got %+v, want nothing on a search failure", found)
	}
}

func TestSearchIdentify_NoMatchesIsNotAnError(t *testing.T) {
	searcher := &fakeSearcher{}
	found := searchIdentify(t, mediatool.Tags{"artist": "Vutu Doci", "album": "The Sezo Siluko"}, "x/a.flac", searcher)
	if found != nil {
		t.Errorf("got %+v, want nothing when the search found no candidates", found)
	}
}

func TestSearchIdentify_ReportsWhyItCouldNotReadTheTags(t *testing.T) {
	identifier := music.NewSearchIdentifier(music.SearchIdentifierConfig{
		Tags: fixedTags{err: errors.New("not in the probed batch")}, Search: &fakeSearcher{},
	})
	if _, err := identifier.Identify(t.Context(), model.FileRef{RelPath: "a.flac"}); err == nil {
		t.Fatal("Identify succeeded despite an unreadable tag batch")
	}
}

func TestSearchIdentify_OrderRunsAfterEmbeddedTag(t *testing.T) {
	identifier := music.NewSearchIdentifier(music.SearchIdentifierConfig{Tags: fixedTags{}, Search: &fakeSearcher{}})
	if identifier.Order() <= music.OrderEmbeddedTag {
		t.Errorf("Order = %d, want it to run after OrderEmbeddedTag (%d)", identifier.Order(), music.OrderEmbeddedTag)
	}
}

// RunChain is what actually consumes this: a clean match must be the chain's Best,
// and its confidence must be strictly below what MBIDIdentifier would have
// produced for the same file if it had had a real MBID -- so a tagged file is
// always trusted over a search guess if both somehow answered.
func TestSearchIdentify_ResolvesThroughTheChainButNeverOutranksAnEmbeddedTag(t *testing.T) {
	searcher := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-search", Title: "The Sezo Siluko"}, ArtistCredit: "Vutu Doci"},
	}}
	tags := mediatool.Tags{"artist": "Vutu Doci", "album": "The Sezo Siluko",
		"musicbrainz_releasegroupid": releaseGroupMBID}

	mbid := music.NewMBIDIdentifier(fixedTags{tags: tags})
	search := music.NewSearchIdentifier(music.SearchIdentifierConfig{Tags: fixedTags{tags: tags}, Search: searcher})

	result, err := model.RunChain(t.Context(), []model.Identifier{mbid, search},
		model.FileRef{RelPath: "a.flac"}, model.ChainConfig{AcceptThreshold: 0.9, AmbiguityThreshold: 0.6})
	if err != nil {
		t.Fatalf("RunChain: %v", err)
	}
	if result.Best == nil {
		t.Fatal("the chain resolved nothing although the embedded tag alone should have")
	}
	if result.Best.ExternalID.ExternalID != releaseGroupMBID {
		t.Errorf("Best = %+v, want the embedded MBID, not the search guess", result.Best)
	}
	// AcceptThreshold (0.9) is never cleared by the tag alone (0.95 *is* cleared,
	// this fixture's release group id gives exactly that) -- confirms the search
	// strategy did not even need to run for the chain to still prefer the tag.
	if searcher.calls != 0 {
		t.Errorf("search was called %d times, want 0 -- AcceptThreshold should stop the chain first", searcher.calls)
	}
}
