package music

import (
	"context"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/match"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// Providers of an identified video work: a music video is its MusicBrainz video recording, a concert is the live
// release group it films. ProviderVideoAlbum additionally ties a music video to the release group of its track.
const (
	ProviderVideoRecording = "musicbrainz-video-recording"
	ProviderLiveVideo      = "musicbrainz-live-video"
	ProviderVideoAlbum     = "musicbrainz-video-album"
)

const (
	videoSearchLimit = 10
	// videoAmbiguity is how close a second recording's confidence must be to leave the choice to a person.
	videoAmbiguity = 0.05
	// videoYearPenalty is taken off a concert whose year is not its live album's.
	videoYearPenalty  = 0.15
	liveSecondaryType = "Live"
)

// VideoLookup is what identifying a video asks MusicBrainz; musicbrainz.Provider implements it.
type VideoLookup interface {
	SearchVideoRecordings(ctx context.Context, priority musicbrainz.Priority, artist, title string, limit int) ([]musicbrainz.RecordingMatch, error)
	Search(ctx context.Context, priority musicbrainz.Priority, artist, title string, limit int) ([]musicbrainz.ReleaseGroupMatch, error)
}

// VideoIdentity is what MusicBrainz says a video is.
type VideoIdentity struct {
	Provider, ExternalID string
	Title                string
	Year                 int
	// ReleaseGroupMBID is the album the video belongs to: a music video's track's, a concert's live album; empty when none.
	ReleaseGroupMBID string
	Confidence       float64
}

// IdentifyVideo names a video from its claim: a music video by a video recording of the artist, a concert by a live
// release group. Nil when nothing reaches threshold or the best is not clearly ahead (the video stays in Unidentified).
func IdentifyVideo(ctx context.Context, lookup VideoLookup, claim VideoClaim, threshold float64) (*VideoIdentity, error) {
	if lookup == nil || claim.Artist == "" || claim.Title == "" {
		return nil, nil
	}
	var candidates []VideoIdentity
	var err error
	if claim.Subtype == SubtypeConcertVideo {
		candidates, err = concertCandidates(ctx, lookup, claim)
	} else {
		candidates, err = musicVideoCandidates(ctx, lookup, claim)
	}
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	slices.SortStableFunc(candidates, func(a, b VideoIdentity) int {
		switch {
		case a.Confidence > b.Confidence:
			return -1
		case a.Confidence < b.Confidence:
			return 1
		}
		return 0
	})
	best := candidates[0]
	if best.Confidence < threshold {
		return nil, nil
	}
	for _, other := range candidates[1:] {
		if other.ExternalID != best.ExternalID && best.Confidence-other.Confidence < videoAmbiguity {
			return nil, nil
		}
	}
	return &best, nil
}

// confidenceOf is one minus the mean of the title's and the artist's edit penalties.
func confidenceOf(titlePenalty, artistPenalty float64) float64 {
	return 1 - (titlePenalty+artistPenalty)/2
}

func musicVideoCandidates(ctx context.Context, lookup VideoLookup, claim VideoClaim) ([]VideoIdentity, error) {
	found, err := lookup.SearchVideoRecordings(ctx, musicbrainz.PriorityIdentify, firstCredit(claim.Artist), titleOrCore(claim.Title), videoSearchLimit)
	if err != nil {
		return nil, err
	}
	var candidates []VideoIdentity
	for _, recording := range found {
		titlePenalty := match.EditPenalty(claim.Title, recording.Title)
		credit := artistPenalty(claim.Artist, recording.ArtistCredit)
		if !sameTrackTitle(claim.Title, recording.Title) || credit > maxArtistPenalty {
			continue
		}
		identity := VideoIdentity{
			Provider: ProviderVideoRecording, ExternalID: recording.RecordingMBID, Title: recording.Title,
			Confidence: confidenceOf(titlePenalty, credit),
		}
		if group, ok := earliestGroup(recording.ReleaseGroups); ok {
			identity.ReleaseGroupMBID, identity.Year = group.MBID, leadingYear(group.FirstReleaseDate)
		}
		candidates = append(candidates, identity)
	}
	return candidates, nil
}

// earliestGroup is the release group of a recording that came first, the original rather than a compilation's reissue.
func earliestGroup(groups []musicbrainz.ReleaseGroup) (musicbrainz.ReleaseGroup, bool) {
	if len(groups) == 0 {
		return musicbrainz.ReleaseGroup{}, false
	}
	return slices.MinFunc(groups, func(a, b musicbrainz.ReleaseGroup) int {
		return compareDates(a.FirstReleaseDate, b.FirstReleaseDate)
	}), true
}

func concertCandidates(ctx context.Context, lookup VideoLookup, claim VideoClaim) ([]VideoIdentity, error) {
	found, err := lookup.Search(ctx, musicbrainz.PriorityIdentify, firstCredit(claim.Artist), claim.Title, videoSearchLimit)
	if err != nil {
		return nil, err
	}
	var candidates []VideoIdentity
	for _, group := range found {
		if !slices.Contains(group.SecondaryTypes, liveSecondaryType) {
			continue
		}
		titlePenalty := match.EditPenalty(claim.Title, group.Title)
		credit := min(artistPenalty(claim.Artist, group.ArtistCredit), artistPenalty(claim.Artist, group.ArtistNames))
		if credit > maxArtistPenalty {
			continue
		}
		confidence := confidenceOf(titlePenalty, credit)
		year := leadingYear(group.FirstReleaseDate)
		if claim.Year != 0 && year != 0 && (claim.Year-year > 1 || year-claim.Year > 1) {
			confidence -= videoYearPenalty
		}
		candidates = append(candidates, VideoIdentity{
			Provider: ProviderLiveVideo, ExternalID: group.MBID, Title: group.Title, Year: year,
			ReleaseGroupMBID: group.MBID, Confidence: confidence,
		})
	}
	return candidates, nil
}

// titleOrCore is the title without its bracketed qualifiers, which the search phrase must not carry.
func titleOrCore(title string) string {
	if core := titleCore(title); core != "" {
		return core
	}
	return title
}

// WantedVideoIdentity is the identity of a wanted video work: its recording or live release group, with full confidence; nil for a work that is not one.
func WantedVideoIdentity(detail library.WorkDetail) *VideoIdentity {
	if !detail.Work.Subtype.Valid || !slices.Contains(VideoSubtypes(), detail.Work.Subtype.String) {
		return nil
	}
	for _, external := range detail.ExternalIDs {
		if external.Provider == ProviderVideoRecording || external.Provider == ProviderLiveVideo {
			return &VideoIdentity{Provider: external.Provider, ExternalID: external.ExternalID, Title: detail.Work.CanonicalTitle, Confidence: 1}
		}
	}
	return nil
}
