package music_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/makearr/MediaNexus/internal/media/music"
	"github.com/makearr/MediaNexus/internal/metadata/musicbrainz"
)

type trackSpec struct {
	title   string
	seconds int
}

var traumaTracks = []trackSpec{
	{"Kebilegev Bibihi", 250}, {"Semidos", 225}, {"Kuselobi You Pato", 244},
	{"Tusa Behif Us", 270}, {"But We Kefo It", 234}, {"Kunihop", 216},
}

func tagged(title string) music.Claim {
	return music.Claim{Value: title, Provenance: music.ProvenanceTag}
}

// folderOf is a folder holding the given tracks, tagged the way a clean rip is.
func folderOf(tracks []trackSpec) []music.LocalTrack {
	locals := make([]music.LocalTrack, len(tracks))
	for i, track := range tracks {
		locals[i] = music.LocalTrack{
			Title: tagged(track.title), Artist: tagged("P!po"), Album: tagged("Kebilegev Bibihi"),
			Track: i + 1, Duration: time.Duration(track.seconds) * time.Second,
		}
	}
	return locals
}

func releaseOf(mbid, title, status, date string, tracks []trackSpec) musicbrainz.Release {
	medium := musicbrainz.Medium{Position: 1}
	for i, track := range tracks {
		medium.Tracks = append(medium.Tracks, musicbrainz.Track{
			Position: i + 1, Title: track.title, DurationMs: int64(track.seconds) * 1000,
			RecordingMBID: fmt.Sprintf("recording-%s", track.title),
		})
	}
	return musicbrainz.Release{MBID: mbid, Title: title, Status: status, Date: date, Media: []musicbrainz.Medium{medium}}
}

func candidateOf(rgMBID, primary string, secondary []string, release musicbrainz.Release) music.AlbumCandidate {
	return music.AlbumCandidate{
		ReleaseGroup: musicbrainz.ReleaseGroup{MBID: rgMBID, Title: release.Title, PrimaryType: primary, SecondaryTypes: secondary},
		ArtistCredit: "P!po", Release: release, Recall: music.RecallAlbumTags,
	}
}

func bestReleaseGroup(decision music.AlbumDecision) string {
	if decision.Best == nil {
		return ""
	}
	return decision.Best.Candidate.ReleaseGroup.MBID
}

func TestDecideAlbum_NoCandidates(t *testing.T) {
	if got := music.DecideAlbum(folderOf(traumaTracks), nil); got.Outcome != music.AlbumNoCandidate || got.Best != nil {
		t.Errorf("decision = %+v, want no_candidate", got)
	}
}

// The same recordings on a compilation must not take the folder from the studio album.
func TestDecideAlbum_AStudioAlbumBeatsACompilationOfTheSameRecordings(t *testing.T) {
	studio := candidateOf("rg-studio", "Album", nil, releaseOf("rel-studio", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	compilation := candidateOf("rg-compilation", "Album", []string{"Compilation"},
		releaseOf("rel-compilation", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{compilation, studio})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-studio" {
		t.Fatalf("decision = %s best %q, want identified rg-studio", decision.Outcome, bestReleaseGroup(decision))
	}
	if !decision.Scores[1].Accepted {
		t.Error("the compilation was refused, want it accepted but ranked below: release type ranks, never gates")
	}
}

// Two different albums fitting equally well are a coin flip, not an identification.
func TestDecideAlbum_TwoReleaseGroupsFittingEquallyAreAmbiguous(t *testing.T) {
	first := candidateOf("rg-a", "Album", nil, releaseOf("rel-a", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	second := candidateOf("rg-b", "Album", nil, releaseOf("rel-b", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{first, second})
	if decision.Outcome != music.AlbumAmbiguous || decision.Best != nil {
		t.Errorf("decision = %s best %q, want ambiguous with no best", decision.Outcome, bestReleaseGroup(decision))
	}
}

// A live album carries the same titles at very different lengths: the tags point at it, the audio refutes it.
func TestDecideAlbum_SameTitlesAtFarOffLengthsAreContradictory(t *testing.T) {
	live := make([]trackSpec, len(traumaTracks))
	for i, track := range traumaTracks {
		live[i] = trackSpec{track.title, track.seconds + 90}
	}
	candidate := candidateOf("rg-live", "Album", []string{"Live"}, releaseOf("rel-live", "Kebilegev Bibihi", "official", "2019-01-01", live))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{candidate})
	if decision.Outcome != music.AlbumContradictory || !decision.Scores[0].Contradicted {
		t.Errorf("decision = %s contradicted=%v, want contradictory", decision.Outcome, decision.Scores[0].Contradicted)
	}
}

// One file with a hidden track appended must not veto a whole album.
func TestDecideAlbum_OneFarOffLengthDoesNotVetoTheAlbum(t *testing.T) {
	locals := folderOf(traumaTracks)
	locals[5].Duration += 5 * time.Minute
	candidate := candidateOf("rg-studio", "Album", nil, releaseOf("rel-studio", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{candidate})
	if decision.Outcome != music.AlbumIdentified {
		t.Fatalf("decision = %s (gate %.3f, mapped %d), want identified", decision.Outcome, decision.Scores[0].GateDistance, decision.Scores[0].Mapped)
	}
	if decision.Best.Tracks[5] != nil {
		t.Errorf("the far-off file was paired with %+v, want it left unpaired", decision.Best.Tracks[5])
	}
}

// Missing tracks never count: half an album still matches its own release.
func TestDecideAlbum_APartialFolderStillMatchesItsAlbum(t *testing.T) {
	locals := folderOf(traumaTracks[:4])
	candidate := candidateOf("rg-studio", "Album", nil, releaseOf("rel-studio", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{candidate})
	if decision.Outcome != music.AlbumIdentified || decision.Best.Distance != 0 {
		t.Errorf("decision = %s distance %v, want identified at distance 0", decision.Outcome, decision.Scores[0].Distance)
	}
}

// Positions and lengths alone agree with far too many albums to identify one on their own.
func TestDecideAlbum_ALoneCandidateWithoutTitlesIsBelowQuorum(t *testing.T) {
	locals := folderOf(traumaTracks)
	for i := range locals {
		locals[i].Title = music.Claim{}
	}
	candidate := candidateOf("rg-studio", "Album", nil, releaseOf("rel-studio", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{candidate})
	if decision.Outcome != music.AlbumInsufficientEvidence || decision.Scores[0].Accepted {
		t.Errorf("decision = %s accepted=%v, want refused as insufficient_evidence", decision.Outcome, decision.Scores[0].Accepted)
	}
}

// Editions sharing a tracklist tie on distance; the one whose track count fits the folder wins.
func TestDecideAlbum_TheEditionClosestToTheFolderWins(t *testing.T) {
	deluxeTracks := append(append([]trackSpec{}, traumaTracks...), trackSpec{"Bonus One", 200}, trackSpec{"Bonus Two", 210})
	deluxe := candidateOf("rg-studio", "Album", nil, releaseOf("rel-deluxe", "Kebilegev Bibihi (Deluxe)", "official", "2017-10-01", deluxeTracks))
	standard := candidateOf("rg-studio", "Album", nil, releaseOf("rel-standard", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{deluxe, standard})
	if decision.Outcome != music.AlbumIdentified || decision.Best.Candidate.Release.MBID != "rel-standard" {
		t.Errorf("decision = %s release %+v, want identified rel-standard", decision.Outcome, decision.Best)
	}
}

// A file already carrying a different recording id is that recording, however well the title fits.
func TestDecideAlbum_AConflictingRecordingIDIsNeverPaired(t *testing.T) {
	locals := folderOf(traumaTracks)
	locals[0].RecordingMBID = "recording-somewhere-else"
	candidate := candidateOf("rg-studio", "Album", nil, releaseOf("rel-studio", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{candidate})
	if score := decision.Scores[0]; score.Tracks[0] != nil || score.Mapped != len(locals)-1 {
		t.Errorf("track 0 paired with %+v (mapped %d), want it unpaired", score.Tracks[0], score.Mapped)
	}
}

// Another artist's album of the same name is refused outright, not merely ranked lower.
func TestDecideAlbum_AClearlyDifferentArtistIsRefused(t *testing.T) {
	candidate := candidateOf("rg-other", "Album", nil, releaseOf("rel-other", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	candidate.ArtistCredit = "Metallica"

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{candidate})
	if decision.Scores[0].Accepted || decision.Outcome != music.AlbumInsufficientEvidence {
		t.Errorf("decision = %s accepted=%v, want refused", decision.Outcome, decision.Scores[0].Accepted)
	}
}

// A weak release ranked first must not hide a release further down that the files actually support.
func TestDecideAlbum_TheSupportedReleaseWinsOverACloserUnsupportedOne(t *testing.T) {
	locals := []music.LocalTrack{{
		Title: tagged("Mic You Pamoh (Cese Mufat remix)"), Artist: tagged("Guvih Rogol"), Album: tagged("Mic You Pamoh (2008 mix)"),
		Track: 1, Duration: 196 * time.Second,
	}}
	original := music.AlbumCandidate{
		ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-original", Title: "Mic You Pamoh", PrimaryType: "Single"}, ArtistCredit: "Guvih Rogol",
		Release: releaseOf("rel-original", "Mic You Pamoh", "official", "2008-07-13", []trackSpec{{"Mic You Pamoh", 190}}),
	}
	remix := music.AlbumCandidate{
		ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-remix", Title: "Mic You Pamoh (2008 mix)", PrimaryType: "Single", SecondaryTypes: []string{"Remix"}},
		ArtistCredit: "Guvih Rogol",
		Release:      releaseOf("rel-remix", "Mic You Pamoh (2008 mix)", "official", "2008", []trackSpec{{"Mic You Pamoh (Cese Mufat remix)", 197}, {"Mic You Pamoh (Moto Blanco radio edit)", 238}}),
	}

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{original, remix})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-remix" {
		t.Errorf("decision = %s %q, want identified rg-remix", decision.Outcome, bestReleaseGroup(decision))
	}
}

// Two release groups fitting equally, one known only from a promotional release: the official one wins.
func TestDecideAlbum_AnOfficialReleaseBeatsAnEquallyFittingPromo(t *testing.T) {
	official := candidateOf("rg-official", "Album", nil, releaseOf("rel-official", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	promo := candidateOf("rg-promo", "Album", nil, releaseOf("rel-promo", "Kebilegev Bibihi", "promotion", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{promo, official})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-official" {
		t.Errorf("decision = %s %q, want identified rg-official", decision.Outcome, bestReleaseGroup(decision))
	}
}

// Enough refuted files veto a release the remaining files would otherwise carry over the gate.
func TestDecideAlbum_EnoughRefutedFilesVetoAnOtherwiseAcceptedRelease(t *testing.T) {
	tracks := append(append([]trackSpec{}, traumaTracks...), trackSpec{"For Now", 245}, trackSpec{"Secrets", 220})
	locals := folderOf(tracks)
	for _, i := range []int{1, 3, 5} {
		locals[i].Duration += 90 * time.Second
	}
	candidate := candidateOf("rg-studio", "Album", nil, releaseOf("rel-studio", "Kebilegev Bibihi", "official", "2017-10-13", tracks))

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{candidate})
	score := decision.Scores[0]
	if score.GateDistance > 0.20 || float64(score.Mapped)/float64(len(locals)) < 0.6 {
		t.Fatalf("gate %.3f mapped %d/%d: the fixture no longer isolates the veto", score.GateDistance, score.Mapped, len(locals))
	}
	if decision.Outcome != music.AlbumContradictory || score.Accepted {
		t.Errorf("decision = %s accepted=%v, want contradictory and refused", decision.Outcome, score.Accepted)
	}
}
