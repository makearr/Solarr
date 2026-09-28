package music_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
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

// recordedAs gives a release its own recordings: another album with the same titles and lengths.
func recordedAs(release musicbrainz.Release, prefix string) musicbrainz.Release {
	for m := range release.Media {
		for t := range release.Media[m].Tracks {
			release.Media[m].Tracks[t].RecordingMBID = prefix + "-" + release.Media[m].Tracks[t].RecordingMBID
		}
	}
	return release
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

// Found on the main library (2026-09-25): a greatest-hits album, uncontested and fitting, went to
// review because its release type counted against its confidence. Release type only ranks.
func TestDecideAlbum_ALoneFittingCompilationIsNotSentToReview(t *testing.T) {
	compilation := candidateOf("rg-hits", "Album", []string{"Compilation"},
		releaseOf("rel-hits", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{compilation}).Within(0.93)
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-hits" {
		t.Errorf("decision = %s best %q, want identified rg-hits", decision.Outcome, bestReleaseGroup(decision))
	}
}

// Two different albums fitting equally well are a coin flip, not an identification.
func TestDecideAlbum_TwoReleaseGroupsFittingEquallyAreAmbiguous(t *testing.T) {
	first := candidateOf("rg-a", "Album", nil, releaseOf("rel-a", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	second := candidateOf("rg-b", "Album", nil, recordedAs(releaseOf("rel-b", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks), "b"))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{first, second})
	if decision.Outcome != music.AlbumAmbiguous || decision.Best != nil {
		t.Errorf("decision = %s best %q, want ambiguous with no best", decision.Outcome, bestReleaseGroup(decision))
	}
}

// A folder filling one release exactly is that release, not a longer rival it only partly covers
// (the Nak Nime' 12-track "The Nak Nime" against a 26-track one of the same name, the main library).
func TestDecideAlbum_TheOneReleaseAFolderFillsExactlyBeatsALongerRival(t *testing.T) {
	longer := slices.Clone(traumaTracks)
	for i := range 10 {
		longer = append(longer, trackSpec{fmt.Sprintf("Bonus %d", i+1), 200})
	}
	complete := candidateOf("rg-complete", "Album", []string{"Compilation"}, releaseOf("rel-complete", "Kebilegev Bibihi", "official", "2001", traumaTracks))
	partial := candidateOf("rg-partial", "Album", []string{"Compilation"}, releaseOf("rel-partial", "Kebilegev Bibihi", "official", "2003", longer))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{partial, complete})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-complete" {
		t.Errorf("decision = %s %q, want identified rg-complete", decision.Outcome, bestReleaseGroup(decision))
	}
}

// Completeness never overrides a closer fit: Hofun Bogilu Monufinam's 23 files fit the 43-track
// soundtrack exactly and a 23-track album of another year slightly worse (the main library).
func TestDecideAlbum_ACompleteReleaseFittingWorseDoesNotWin(t *testing.T) {
	longer := slices.Clone(traumaTracks)
	for i := range 10 {
		longer = append(longer, trackSpec{fmt.Sprintf("Bonus %d", i+1), 200})
	}
	longer[1].seconds += 15 // neither fits perfectly, or the perfect one would simply win
	completeTracks := slices.Clone(traumaTracks)
	completeTracks[0].seconds += 20
	complete := candidateOf("rg-complete", "Album", nil, releaseOf("rel-complete", "Kebilegev Bibihi", "official", "2017", completeTracks))
	partial := candidateOf("rg-partial", "Album", nil, releaseOf("rel-partial", "Kebilegev Bibihi", "official", "2017", longer))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{complete, partial})
	if decision.Outcome != music.AlbumAmbiguous {
		t.Errorf("decision = %s %q, want ambiguous", decision.Outcome, bestReleaseGroup(decision))
	}
}

// A folder filling neither of two rivals exactly is still theirs to choose between.
func TestDecideAlbum_APartialFolderBetweenTwoRivalsStaysAmbiguous(t *testing.T) {
	folder := folderOf(traumaTracks[:4])
	first := candidateOf("rg-a", "Album", nil, releaseOf("rel-a", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	second := candidateOf("rg-b", "Album", nil, recordedAs(releaseOf("rel-b", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks), "b"))

	decision := music.DecideAlbum(folder, []music.AlbumCandidate{first, second})
	if decision.Outcome != music.AlbumAmbiguous {
		t.Errorf("decision = %s %q, want ambiguous", decision.Outcome, bestReleaseGroup(decision))
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

// A remix single titled like the original ranks below it, as a live release does: one file of
// "Kamo Rigevo" is the single, not its remix release (the main library, 2026-09-27).
func TestDecideAlbum_TheOriginalSingleBeatsARemixReleaseOfTheSameTitle(t *testing.T) {
	locals := []music.LocalTrack{{Title: tagged("Kamo Rigevo"), Artist: tagged("P!po"), Album: tagged("Kamo Rigevo"), Track: 1, Duration: 194 * time.Second}}
	original := candidateOf("rg-single", "Single", nil, releaseOf("rel-single", "Kamo Rigevo", "official", "2024", []trackSpec{{"Kamo Rigevo", 194}}))
	remix := candidateOf("rg-remix", "Single", []string{"Remix"},
		recordedAs(releaseOf("rel-remix", "Kamo Rigevo", "official", "2024", []trackSpec{{"Kamo Rigevo", 195}}), "remix"))

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{remix, original})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-single" {
		t.Errorf("decision = %s %q, want identified rg-single", decision.Outcome, bestReleaseGroup(decision))
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

// Found on the main library (2026-09-25): 2Ibe's live album is credited "Tupac". A tag naming the
// artist by their own name still fits.
func TestDecideAlbum_ATagNamingTheArtistFitsACreditSpellingItOtherwise(t *testing.T) {
	candidate := candidateOf("rg-live", "Album", []string{"Live"},
		releaseOf("rel-live", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	candidate.ArtistCredit, candidate.ArtistNames = "Alecia Moore", "P!po"

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{candidate})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-live" {
		t.Errorf("decision = %s best %q, want identified rg-live", decision.Outcome, bestReleaseGroup(decision))
	}
}

// One album catalogued as two release groups -- same length, same recordings -- is identified,
// not put to the user (Hall & Oates' "Backtracks" and Sobo Mutuke's "Halek", the main library).
func TestDecideAlbum_OneAlbumCataloguedTwiceIsIdentified(t *testing.T) {
	first := candidateOf("rg-a", "Album", nil, releaseOf("rel-a", "Kebilegev Bibihi", "official", "2017-06-08", traumaTracks))
	second := candidateOf("rg-b", "Album", nil, releaseOf("rel-b", "Kebilegev Bibihi", "official", "2017-12-13", traumaTracks))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{second, first})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-a" {
		t.Errorf("decision = %s %q, want identified rg-a, the earlier", decision.Outcome, bestReleaseGroup(decision))
	}
}

// A two-disc set tagged disc 3 throughout scores as if untagged: two files claiming the same disc
// and track prove the disc claims wrong, so they abstain (NETA' "Kick", the main library).
func TestDecideAlbum_DiscClaimsThatCollideAbstain(t *testing.T) {
	release := releaseOf("rel-set", "Kebilegev Bibihi", "official", "2017-10-13", nil)
	for disc, tracks := range [][]trackSpec{traumaTracks[:3], traumaTracks[3:]} {
		medium := releaseOf("", "", "", "", tracks).Media[0]
		medium.Position = disc + 1
		release.Media = append(release.Media, medium)
	}
	candidates := []music.AlbumCandidate{candidateOf("rg-set", "Album", nil, release)}
	untagged, wrong := folderOf(traumaTracks), folderOf(traumaTracks)
	for i := range traumaTracks {
		untagged[i].Track, wrong[i].Track = i%3+1, i%3+1
		wrong[i].Disc = 3
	}

	want := music.DecideAlbum(untagged, candidates).Scores[0].Distance
	if got := music.DecideAlbum(wrong, candidates).Scores[0].Distance; got != want {
		t.Errorf("distance = %.3f, want %.3f as with no disc tags", got, want)
	}
}

// The folder naming the album fits it as well as a correct tag would.
func TestDecideAlbum_TheFoldersAlbumNameFitsWhereTheTagDoesNot(t *testing.T) {
	candidates := []music.AlbumCandidate{candidateOf("rg-studio", "Album", nil, studioRelease)}
	malformed := folderOf(traumaTracks)
	for i := range malformed {
		malformed[i].Album = tagged("P!po / Kebilegev Bibihi - Live / Remixed")
		malformed[i].FolderAlbum = "Kebilegev Bibihi"
	}

	want := music.DecideAlbum(folderOf(traumaTracks), candidates).Scores[0].Distance
	if got := music.DecideAlbum(malformed, candidates).Scores[0].Distance; got != want {
		t.Errorf("distance = %.3f, want %.3f as with a correct tag", got, want)
	}
}

// The album the files name decides between close rivals: "Kerul of the Fofu" is not "1958 Bokuf",
// however much a studio album is preferred to a compilation (the main library, 2026-09-27).
func TestDecideAlbum_TheAlbumTheFilesNameWinsInsideTheMargin(t *testing.T) {
	studio := candidateOf("rg-studio", "Album", nil, studioRelease)
	named := candidateOf("rg-named", "Album", []string{"Compilation"},
		recordedAs(releaseOf("rel-named", "Kuselobi You Pato", "official", "2017-10-13", traumaTracks), "named"))
	locals := folderOf(traumaTracks)
	for i := range locals {
		locals[i].Album = tagged("Kuselobi You Pato")
	}

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{studio, named})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-named" {
		t.Errorf("decision = %s %q (distances %.3f, %.3f), want identified rg-named", decision.Outcome,
			bestReleaseGroup(decision), decision.Scores[0].Distance, decision.Scores[1].Distance)
	}
}

// A release every claim agrees with beats a rival one claim contradicts, however small the gap
// (Dacapa Hivop Pibazi's "Mepukafamo" at 0.000 against "Mepukafamo" at 0.042, the main library).
func TestDecideAlbum_APerfectFitBeatsACloseImperfectRival(t *testing.T) {
	perfect := candidateOf("rg-perfect", "Album", nil, releaseOf("rel-perfect", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	rival := candidateOf("rg-close", "Album", nil, recordedAs(releaseOf("rel-close", "Kebilegev Bibihi: Live", "official", "2017-10-13", traumaTracks), "live"))

	decision := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{rival, perfect})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-perfect" {
		t.Errorf("decision = %s %q (distances %.3f, %.3f), want identified rg-perfect", decision.Outcome, bestReleaseGroup(decision),
			decision.Scores[0].Distance, decision.Scores[1].Distance)
	}
}

// Pressings sharing a tracklist share its pairing, never its title or date: each is still judged
// on its own (the pairing is computed once per tracklist, 2026-09-26).
func TestDecideAlbum_PressingsSharingATracklistKeepTheirOwnTitles(t *testing.T) {
	right := candidateOf("rg-a", "Album", nil, releaseOf("rel-right", "Kebilegev Bibihi", "official", "2017-10-13", traumaTracks))
	renamed := candidateOf("rg-a", "Album", nil, releaseOf("rel-renamed", "Something Else Entirely", "official", "2017-10-13", traumaTracks))
	renamed.ReleaseGroup.Title = "Something Else Entirely"

	scores := music.DecideAlbum(folderOf(traumaTracks), []music.AlbumCandidate{renamed, right}).Scores
	if scores[0].Candidate.Release.MBID != "rel-right" || scores[0].Distance >= scores[1].Distance {
		t.Errorf("scores = %s %.3f, %s %.3f; want rel-right first and closer",
			scores[0].Candidate.Release.MBID, scores[0].Distance, scores[1].Candidate.Release.MBID, scores[1].Distance)
	}
	if scores[0].Mapped != len(traumaTracks) || scores[1].Mapped != len(traumaTracks) {
		t.Errorf("mapped = %d, %d; want every file paired on both", scores[0].Mapped, scores[1].Mapped)
	}
}

// A second copy of a track is placed on the track it copies, without counting toward the fit:
// four duplicated "Buhobuv Rezid" tracks, left unplaced, went on to a demo bootleg.
func TestDecideAlbum_ASecondCopyIsPlacedOnTheTrackItCopies(t *testing.T) {
	candidates := []music.AlbumCandidate{candidateOf("rg-studio", "Album", nil, studioRelease)}
	folder := append(folderOf(traumaTracks), folderOf(traumaTracks[:1])...)

	decision := music.DecideAlbum(folder, candidates)
	score := decision.Scores[0]
	dup := len(folder) - 1
	if score.Tracks[dup] == nil || score.Tracks[dup].Title != traumaTracks[0].title {
		t.Errorf("second copy placed on %+v, want %q", score.Tracks[dup], traumaTracks[0].title)
	}
	if score.Mapped != len(traumaTracks) || decision.Outcome != music.AlbumIdentified {
		t.Errorf("mapped %d, outcome %s; want %d mapped and the album identified", score.Mapped, decision.Outcome, len(traumaTracks))
	}
}

// A file no track fits stays unplaced, second copies or not.
func TestDecideAlbum_AFileNoTrackFitsStaysUnplaced(t *testing.T) {
	candidates := []music.AlbumCandidate{candidateOf("rg-studio", "Album", nil, studioRelease)}
	folder := append(folderOf(traumaTracks), folderOf([]trackSpec{{"Hidden Bonus", 400}})...)

	score := music.DecideAlbum(folder, candidates).Scores[0]
	if track := score.Tracks[len(folder)-1]; track != nil {
		t.Errorf("bonus track placed on %+v, want unplaced", track)
	}
}

// The untagged rest of a folder whose tagged files name the album only has to fit it, not prove it
// again: one odd length among four files sent "Kab It for the Mazar" to review (the main library).
func TestWithin_TheSiblingsAlbumIsNotSentToReviewForAWeakFit(t *testing.T) {
	weak := func(recall string) music.AlbumDecision {
		candidate := candidateOf("rg-studio", "Album", nil, studioRelease)
		candidate.Recall = recall
		best := music.ReleaseScore{Candidate: candidate, Accepted: true, Distance: 0.18, GateDistance: 0.19}
		return music.AlbumDecision{Outcome: music.AlbumIdentified, Scores: []music.ReleaseScore{best}, Best: &best}
	}
	if got := weak(music.RecallSibling).Within(0.9); got.Outcome != music.AlbumIdentified {
		t.Errorf("siblings' album = %s, want identified", got.Outcome)
	}
	if got := weak(music.RecallAlbumTags).Within(0.9); got.Outcome != music.AlbumAmbiguous {
		t.Errorf("album recalled by its tags = %s, want ambiguous below the threshold", got.Outcome)
	}
}

// A playlist holding a compilation's tracks in its own order is not that compilation: XT/QK's
// "Essentials" fitted the box set "The Collection" 28 files of 30 (the main library, 2026-09-27).
func TestDecideAlbum_APlaylistIsNotTheCompilationThatHoldsItsTracks(t *testing.T) {
	names := []string{"Thunder", "Jailbreak", "Problem", "Legofebato", "Heatseeker", "Safe", "Rock", "Train",
		"Shoot", "Whole", "Bells", "Highway", "Dirty", "Blood", "Flames", "Touch", "Hizeh", "Bust", "Jack", "Rosie"}
	hits := make([]trackSpec, len(names))
	for i, name := range names {
		hits[i] = trackSpec{name, 150 + 13*i}
	}
	compilation := candidateOf("rg-collection", "Album", []string{"Compilation"},
		releaseOf("rel-collection", "The Collection", "official", "2005", hits))
	playlist := folderOf(hits)
	for i := range playlist {
		playlist[i].Album = tagged("Essentials")
		playlist[i].Track = (i+1)%len(playlist) + 1
	}

	if decision := music.DecideAlbum(playlist, []music.AlbumCandidate{compilation}); decision.Outcome == music.AlbumIdentified {
		t.Errorf("decision = %s %q, want the playlist left to be identified track by track", decision.Outcome, bestReleaseGroup(decision))
	}
	inOrder := folderOf(hits)
	for i := range inOrder {
		inOrder[i].Album = tagged("Essentials")
	}
	if decision := music.DecideAlbum(inOrder, []music.AlbumCandidate{compilation}); decision.Outcome != music.AlbumIdentified {
		t.Errorf("in its own order = %s, want the compilation identified", decision.Outcome)
	}
}

// The release the files name beats one they do not beyond the margin too: two files of a
// "50th Anniversary Remix" went to the 1969 original on release type alone (Rehekan, 2026-09-28).
func TestDecideAlbum_TheNamedReleaseBeatsAnUnnamedPreferredOne(t *testing.T) {
	original := candidateOf("rg-original", "Album", nil, studioRelease)
	remix := candidateOf("rg-remix", "Album", []string{"Remix"},
		recordedAs(releaseOf("rel-remix", "Kebilegev Bibihi: 50th Anniversary Remix", "official", "2019", traumaTracks), "remix"))
	locals := folderOf(traumaTracks[:2])
	for i := range locals {
		locals[i].Album = tagged("Kebilegev Bibihi: 50th Anniversary Remix")
	}

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{original, remix})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-remix" {
		t.Errorf("decision = %s %q (distances %.3f, %.3f), want identified rg-remix", decision.Outcome, bestReleaseGroup(decision),
			decision.Scores[0].Distance, decision.Scores[1].Distance)
	}
}

// An album tag names the release it spells out, never one its cleaned form happens to spell: "LESA:
// Vs." is Safav Jam's 2023 remix, not the 1993 "Vs." the cleaned "Vs." would name (2026-09-28).
func TestDecideAlbum_TheTagAsWrittenNamesTheRelease(t *testing.T) {
	original := candidateOf("rg-original", "Album", nil, releaseOf("rel-original", "Vs.", "official", "1993", traumaTracks))
	remix := candidateOf("rg-deep", "Album", []string{"Compilation", "Live"},
		recordedAs(releaseOf("rel-deep", "LESA: Vs.", "official", "2023", traumaTracks), "deep"))
	locals := folderOf(traumaTracks[:2])
	for i := range locals {
		locals[i].Album = tagged("LESA: Vs.")
		locals[i].FolderAlbum = "Vs." // "LESA - Vs. (2023)" parses as artist "DEEP", album "Vs."
	}

	decision := music.DecideAlbum(locals, []music.AlbumCandidate{original, remix})
	if decision.Outcome != music.AlbumIdentified || bestReleaseGroup(decision) != "rg-deep" {
		t.Errorf("decision = %s %q, want identified rg-deep", decision.Outcome, bestReleaseGroup(decision))
	}
}
