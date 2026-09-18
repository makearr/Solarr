package music

import (
	"cmp"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/identify/match"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// Album matching scores a folder against whole tracklists: documentation/music-identification.md.
const (
	// albumAcceptDistance is the most a release's gate distance may be and still be accepted.
	albumAcceptDistance = 0.20
	// minMappedFraction of the folder's files must land on a track of the release.
	minMappedFraction = 0.6
	// maxArtistPenalty beyond which the credited artist is clearly someone else.
	maxArtistPenalty = 0.4
	// winnerMargin the best release must lead a different release group's best by.
	winnerMargin = 0.05
	// supportTitlePenalty and supportPairDistance bound a pair that counts as supported.
	supportTitlePenalty = 0.35
	supportPairDistance = 0.40
	// durationGrace is ignored; the penalty then ramps to full over durationWindow.
	durationGrace  = 10 * time.Second
	durationWindow = 30 * time.Second
	// durationConflict apart, a same-named track is a different recording (a live take).
	durationConflict = 30 * time.Second
	// maxContradictedShare of a folder's files may be refuted before the release is vetoed,
	// so one file with a hidden track appended cannot sink a whole album.
	maxContradictedShare = 0.25
	// unmappedCost is what the assignment pays to leave a file or a track unpaired.
	unmappedCost = 2.0
	// parsedDampening halves how much a value parsed from a path counts against a tag.
	parsedDampening = 0.5
	// distanceEpsilon treats floating-point near-equal distances as a tie.
	distanceEpsilon = 1e-6
	// variousArtistsMBID is MusicBrainz's special "Various Artists" artist.
	variousArtistsMBID = "89ad4ac3-39f7-470e-963a-56509c546377"
)

// Provenance is where a claimed value came from; weaker evidence counts for less.
type Provenance int

const (
	// ProvenanceAbsent means nothing claimed a value.
	ProvenanceAbsent Provenance = iota
	// ProvenancePlaceholder is a value that says nothing ("Unknown Artist"); it abstains.
	ProvenancePlaceholder
	// ProvenanceParsed is a value read from the file or folder name.
	ProvenanceParsed
	// ProvenanceTag is a value read from the file's own tags.
	ProvenanceTag
)

// Claim is one value a file makes about itself.
type Claim struct {
	Value      string
	Provenance Provenance
}

// present reports whether the claim may take part in matching at all.
func (c Claim) present() bool {
	return c.Value != "" && (c.Provenance == ProvenanceTag || c.Provenance == ProvenanceParsed)
}

// LocalTrack is one file of a folder, as album matching sees it.
type LocalTrack struct {
	Title, Artist, Album Claim
	Disc, Track          int
	Year                 int
	Duration             time.Duration
	RecordingMBID        string
}

// AlbumCandidate is one release to score, with the release group it belongs to.
type AlbumCandidate struct {
	ReleaseGroup musicbrainz.ReleaseGroup
	ArtistCredit string
	Release      musicbrainz.Release
	// Recall is how the release group was found: sibling, album_tags or recording_search.
	Recall string
}

// dimension is one weighted part of a distance; the order fixes the summation order.
type dimension int

const (
	dimensionArtist dimension = iota
	dimensionAlbum
	dimensionYear
	dimensionTracks
	dimensionMissingTracks
	dimensionUnmatchedTracks
	dimensionTrackTitle
	dimensionTrackLength
	dimensionTrackIndex
	dimensionRecordingID
	dimensionReleaseType
	dimensionCount
)

var dimensionWeights = [dimensionCount]float64{
	dimensionArtist: 3, dimensionAlbum: 3, dimensionYear: 1, dimensionTracks: 2,
	dimensionMissingTracks: 0.6, dimensionUnmatchedTracks: 0.9,
	dimensionTrackTitle: 3, dimensionTrackLength: 2, dimensionTrackIndex: 1, dimensionRecordingID: 10,
	dimensionReleaseType: 3,
}

// distance collects penalties in [0,1] per dimension; normalized is their weighted mean.
type distance struct {
	penalties [dimensionCount][]float64
}

func (d *distance) add(dim dimension, penalty float64) {
	d.penalties[dim] = append(d.penalties[dim], penalty)
}

func (d *distance) addBool(dim dimension, mismatch bool) {
	penalty := 0.0
	if mismatch {
		penalty = 1
	}
	d.add(dim, penalty)
}

func (d distance) normalized(exclude ...dimension) float64 {
	var sum, weight float64
	for dim := range dimensionCount {
		if slices.Contains(exclude, dim) {
			continue
		}
		for _, penalty := range d.penalties[dim] {
			sum += penalty * dimensionWeights[dim]
			weight += dimensionWeights[dim]
		}
	}
	if weight == 0 {
		return 1
	}
	return sum / weight
}

// dampened makes a parsed value's penalty count at half strength, floored so an exact
// parse still stays weaker than an exact tag.
func dampened(penalty float64, provenance Provenance) float64 {
	if provenance != ProvenanceParsed {
		return penalty
	}
	return min(1, max(penalty/parsedDampening, (1-parsedDampening)*0.25))
}

// pairScore is one file against one track.
type pairScore struct {
	cost         float64
	titlePenalty float64
	hasTitles    bool
	conflict     bool
	sameRecord   bool
}

func scorePair(local LocalTrack, track musicbrainz.Track, disc, absolute int) pairScore {
	var d distance
	score := pairScore{}
	if local.Title.present() && track.Title != "" {
		score.hasTitles = true
		score.titlePenalty = match.EditPenalty(local.Title.Value, track.Title)
		d.add(dimensionTrackTitle, dampened(score.titlePenalty, local.Title.Provenance))
	}
	if local.Duration > 0 && track.DurationMs > 0 {
		gap := (local.Duration - time.Duration(track.DurationMs)*time.Millisecond).Abs()
		score.conflict = gap > durationConflict
		d.add(dimensionTrackLength, min(1, max(0, float64(gap-durationGrace)/float64(durationWindow))))
	}
	if local.Track > 0 {
		onPosition := local.Track == track.Position && (local.Disc == 0 || local.Disc == disc)
		d.addBool(dimensionTrackIndex, !onPosition && local.Track != absolute)
	}
	if local.RecordingMBID != "" && track.RecordingMBID != "" {
		score.sameRecord = local.RecordingMBID == track.RecordingMBID
		score.conflict = score.conflict || !score.sameRecord
		d.addBool(dimensionRecordingID, !score.sameRecord)
	}
	score.cost = d.normalized()
	return score
}

// supported reports a pair strong enough to count toward a quorum.
func (s pairScore) supported() bool {
	if s.sameRecord {
		return true
	}
	return s.hasTitles && s.titlePenalty <= supportTitlePenalty && s.cost <= supportPairDistance
}

// ReleaseScore is how well a folder fits one release.
type ReleaseScore struct {
	Candidate AlbumCandidate
	// Distance ranks releases; missing tracks never count, so a partial album still ranks.
	Distance float64
	// GateDistance decides acceptance; release type only ranks, it never gates.
	GateDistance float64
	Mapped       int
	Supported    int
	Accepted     bool
	// Contradicted is a same-named track with a different recording or a length far off.
	Contradicted bool
	// Tracks maps each local file index to its release track, or nil when unpaired.
	Tracks []*ReleaseTrack
}

// ReleaseTrack is one track of a release, located on its medium.
type ReleaseTrack struct {
	Disc          int
	Position      int
	Title         string
	RecordingMBID string
}

func scoreRelease(locals []LocalTrack, candidate AlbumCandidate) ReleaseScore {
	var tracks []ReleaseTrack
	var releaseTracks []musicbrainz.Track
	for _, medium := range candidate.Release.Media {
		for _, track := range medium.Tracks {
			tracks = append(tracks, ReleaseTrack{
				Disc: medium.Position, Position: track.Position, Title: track.Title, RecordingMBID: track.RecordingMBID,
			})
			releaseTracks = append(releaseTracks, track)
		}
	}

	size := max(len(locals), len(tracks))
	cost := make([][]float64, size)
	pairs := make([][]pairScore, len(locals))
	contradictedFiles := 0
	for i := range cost {
		cost[i] = slices.Repeat([]float64{unmappedCost}, size)
	}
	for i, local := range locals {
		pairs[i] = make([]pairScore, len(tracks))
		refuted, fits := false, false
		for j, track := range releaseTracks {
			pair := scorePair(local, track, tracks[j].Disc, j+1)
			pairs[i][j] = pair
			sameName := pair.hasTitles && pair.titlePenalty <= supportTitlePenalty
			if pair.conflict {
				refuted = refuted || sameName
				continue
			}
			fits = fits || sameName
			cost[i][j] = pair.cost
		}
		if refuted && !fits {
			contradictedFiles++
		}
	}
	contradicted := float64(contradictedFiles) > maxContradictedShare*float64(len(locals))

	var d distance
	score := ReleaseScore{Candidate: candidate, Tracks: make([]*ReleaseTrack, len(locals)), Contradicted: contradicted}
	for i, j := range match.Assign(cost) {
		if i >= len(locals) || j >= len(tracks) || pairs[i][j].conflict {
			continue
		}
		score.Tracks[i] = &tracks[j]
		score.Mapped++
		d.add(dimensionTracks, pairs[i][j].cost)
		if pairs[i][j].supported() {
			score.Supported++
		}
	}

	artistOK := addAlbumDimensions(&d, locals, candidate)
	for range min(len(tracks)-score.Mapped, len(locals)) {
		d.add(dimensionMissingTracks, 1)
	}
	for range len(locals) - score.Mapped {
		d.add(dimensionUnmatchedTracks, 1)
	}
	d.add(dimensionReleaseType, releaseTypePenalty(candidate.ReleaseGroup))

	score.Distance = d.normalized(dimensionMissingTracks)
	score.GateDistance = d.normalized(dimensionMissingTracks, dimensionReleaseType)
	// Positions and lengths agree with too many albums; enough titles or recordings must too.
	quorum := score.Supported >= min(2, len(locals))
	score.Accepted = len(locals) > 0 && !contradicted && artistOK && quorum &&
		score.GateDistance <= albumAcceptDistance &&
		float64(score.Mapped)/float64(len(locals)) >= minMappedFraction
	return score
}

// addAlbumDimensions adds artist, album and year, answering whether the artist is plausible.
func addAlbumDimensions(d *distance, locals []LocalTrack, candidate AlbumCandidate) bool {
	artistOK := true
	if artist := consensusClaim(locals, func(t LocalTrack) Claim { return t.Artist }); artist.present() &&
		!isVariousArtists(candidate) && candidate.ArtistCredit != "" {
		penalty := artistPenalty(artist.Value, candidate.ArtistCredit)
		artistOK = penalty <= maxArtistPenalty
		d.add(dimensionArtist, dampened(penalty, artist.Provenance))
	}
	if album := consensusClaim(locals, func(t LocalTrack) Claim { return t.Album }); album.present() {
		penalty := 1.0
		for _, title := range []string{album.Value, cleanAlbumTag(album.Value)} {
			penalty = min(penalty, match.EditPenalty(title, candidate.Release.Title), match.EditPenalty(title, candidate.ReleaseGroup.Title))
		}
		d.add(dimensionAlbum, dampened(penalty, album.Provenance))
	}
	if year, releaseYear := consensusYear(locals), yearOf(candidate.Release.Date); year > 0 && releaseYear > 0 {
		gap := math.Abs(float64(year - releaseYear))
		d.add(dimensionYear, min(1, gap/10))
	}
	return artistOK
}

// artistPenalty is the closest of the whole credit and each credited name, so "A & B"
// still fits a folder tagged "A".
func artistPenalty(local, credit string) float64 {
	best := match.EditPenalty(local, credit)
	for _, name := range splitArtists(credit) {
		best = min(best, match.EditPenalty(local, name))
	}
	for _, name := range splitArtists(local) {
		best = min(best, match.EditPenalty(name, credit))
	}
	return best
}

var artistSeparators = []string{" feat. ", " feat ", " ft. ", " featuring ", " with ", " vs. ", " & ", ", ", " / ", "; ", " x ", " + "}

func splitArtists(credit string) []string {
	parts := []string{strings.ToLower(credit)}
	for _, separator := range artistSeparators {
		var next []string
		for _, part := range parts {
			next = append(next, strings.Split(part, separator)...)
		}
		parts = next
	}
	return parts
}

func isVariousArtists(candidate AlbumCandidate) bool {
	for _, credit := range candidate.ReleaseGroup.ArtistCredit {
		if credit.MBID == variousArtistsMBID {
			return true
		}
	}
	return strings.EqualFold(candidate.ArtistCredit, "Various Artists")
}

// releaseTypePenalty prefers a studio album over an EP, then anything else, then a
// compilation or live release carrying the same recordings.
func releaseTypePenalty(rg musicbrainz.ReleaseGroup) float64 {
	for _, secondary := range rg.SecondaryTypes {
		if strings.EqualFold(secondary, "Compilation") || strings.EqualFold(secondary, "Live") {
			return 1
		}
	}
	switch strings.ToLower(rg.PrimaryType) {
	case "album":
		return 0
	case "ep":
		return 0.3
	}
	return 0.5
}

// consensusClaim is the most common present value; ties go to the stronger provenance, then first seen.
func consensusClaim(locals []LocalTrack, of func(LocalTrack) Claim) Claim {
	counts := make(map[string]int)
	var order []Claim
	for _, local := range locals {
		claim := of(local)
		if !claim.present() {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(claim.Value))
		if counts[key] == 0 {
			order = append(order, claim)
		}
		counts[key]++
	}
	var best Claim
	bestCount := 0
	for _, claim := range order {
		count := counts[strings.ToLower(strings.TrimSpace(claim.Value))]
		if count > bestCount || (count == bestCount && claim.Provenance > best.Provenance) {
			best, bestCount = claim, count
		}
	}
	return best
}

func consensusYear(locals []LocalTrack) int {
	counts := make(map[int]int)
	best, bestCount := 0, 0
	for _, local := range locals {
		if local.Year <= 0 {
			continue
		}
		counts[local.Year]++
		if counts[local.Year] > bestCount {
			best, bestCount = local.Year, counts[local.Year]
		}
	}
	return best
}

// AlbumOutcome names what album matching concluded, so the manual queue can say why.
type AlbumOutcome string

const (
	// AlbumIdentified is one release group, decisively.
	AlbumIdentified AlbumOutcome = "identified"
	// AlbumNoCandidate is nothing recalled at all.
	AlbumNoCandidate AlbumOutcome = "no_candidate"
	// AlbumInsufficientEvidence is candidates, none fitting well enough.
	AlbumInsufficientEvidence AlbumOutcome = "insufficient_evidence"
	// AlbumAmbiguous is two different release groups fitting about equally well.
	AlbumAmbiguous AlbumOutcome = "ambiguous"
	// AlbumContradictory is a release the tags name, but the audio lengths or recordings refute.
	AlbumContradictory AlbumOutcome = "contradictory"
)

// AlbumDecision is album matching's answer: Best is set only when Outcome is identified.
type AlbumDecision struct {
	Outcome AlbumOutcome
	Best    *ReleaseScore
	// Scores is every release scored, accepted first, best first.
	Scores []ReleaseScore
}

// DecideAlbum scores every candidate release against the folder and decides.
func DecideAlbum(locals []LocalTrack, candidates []AlbumCandidate) AlbumDecision {
	if len(candidates) == 0 || len(locals) == 0 {
		return AlbumDecision{Outcome: AlbumNoCandidate}
	}
	scores := make([]ReleaseScore, len(candidates))
	for i, candidate := range candidates {
		scores[i] = scoreRelease(locals, candidate)
	}
	slices.SortStableFunc(scores, func(a, b ReleaseScore) int { return compareScores(a, b, len(locals)) })
	decision := AlbumDecision{Scores: scores}

	best := scores[0]
	if !best.Accepted {
		decision.Outcome = AlbumInsufficientEvidence
		if slices.ContainsFunc(scores, func(s ReleaseScore) bool { return s.Contradicted }) {
			decision.Outcome = AlbumContradictory
		}
		return decision
	}
	runnerUp := slices.IndexFunc(scores, func(s ReleaseScore) bool {
		return s.Accepted && s.Candidate.ReleaseGroup.MBID != best.Candidate.ReleaseGroup.MBID
	})
	// Inside the margin an official release still beats a rival MusicBrainz only knows as a promo or bootleg.
	officialOverUnofficial := best.Candidate.Release.Status == "official" &&
		runnerUp >= 0 && scores[runnerUp].Candidate.Release.Status != "official"
	if runnerUp >= 0 && scores[runnerUp].Distance-best.Distance < winnerMargin && !officialOverUnofficial {
		decision.Outcome = AlbumAmbiguous
		return decision
	}
	decision.Outcome = AlbumIdentified
	decision.Best = &decision.Scores[0]
	return decision
}

// compareScores orders accepted first, then distance, then among equally fitting editions:
// official, track count closest to the folder, earliest date, MBID.
func compareScores(a, b ReleaseScore, localCount int) int {
	if a.Accepted != b.Accepted {
		if a.Accepted {
			return -1
		}
		return 1
	}
	if math.Abs(a.Distance-b.Distance) > distanceEpsilon {
		return cmp.Compare(a.Distance, b.Distance)
	}
	aOfficial, bOfficial := a.Candidate.Release.Status == "official", b.Candidate.Release.Status == "official"
	if aOfficial != bOfficial {
		if aOfficial {
			return -1
		}
		return 1
	}
	aGap, bGap := abs(trackCount(a.Candidate.Release)-localCount), abs(trackCount(b.Candidate.Release)-localCount)
	if aGap != bGap {
		return cmp.Compare(aGap, bGap)
	}
	if aDate, bDate := a.Candidate.Release.Date, b.Candidate.Release.Date; aDate != bDate {
		switch {
		case aDate == "":
			return 1
		case bDate == "":
			return -1
		}
		return cmp.Compare(aDate, bDate)
	}
	return cmp.Compare(a.Candidate.Release.MBID, b.Candidate.Release.MBID)
}

// yearOf reads the year from a partial date such as "2017-10-13".
func yearOf(date string) int {
	if len(date) < 4 {
		return 0
	}
	year := 0
	for _, r := range date[:4] {
		if r < '0' || r > '9' {
			return 0
		}
		year = year*10 + int(r-'0')
	}
	return year
}
