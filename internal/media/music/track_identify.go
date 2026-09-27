package music

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/match"
	"github.com/makearr/Solarr/internal/identify/model"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

// PassTrack identifies a file on its own when its folder did not settle as an album: a playlist,
// a file Lidarr sorted into the wrong album (the folder is a hint, never a certainty).
const PassTrack = "track_match"

// StrategyTrack is the per-track pass's name in an Identification.
const StrategyTrack = "track"

const (
	// trackKnownConfidence is a recording the file's own fingerprint or embedded id names.
	trackKnownConfidence = 0.95
	// trackSearchedConfidence is a recording found by artist, title and a length within trackLengthTolerance.
	trackSearchedConfidence = 0.92
	// trackLengthTolerance is how far a searched recording's length may be from the file's.
	trackLengthTolerance = 5 * time.Second
	// trackTitlePenalty is the most a searched recording's title may differ from the file's.
	trackTitlePenalty = 0.15
	// trackSearchLimit bounds one recording search.
	trackSearchLimit = 10
)

// TrackLookup is what per-track identification asks the metadata sources; musicbrainz.Provider implements it.
type TrackLookup interface {
	SearchRecordings(ctx context.Context, priority musicbrainz.Priority, artist, title string, limit int) ([]musicbrainz.RecordingMatch, error)
	Recording(ctx context.Context, priority musicbrainz.Priority, mbid string) (musicbrainz.RecordingMatch, error)
}

// trackPass identifies each pending file by its recording, then places it on the album that
// recording belongs to: the one the folder or siblings name when the recording is on it, else
// the artist's own earliest studio album before an EP, a single or a compilation.
func (c Consumer) trackPass(ctx context.Context, out *batchResult, files []pendingFile) []pendingFile {
	var hints []string
	if sibling, agreed := soleSiblingIdentity(out.links); agreed && sibling.Provider == ProviderReleaseGroup {
		hints = append(hints, sibling.ExternalID)
	}
	var pending []pendingFile
	for _, file := range files {
		one := out.observed[file.index]
		local := ReadLocalTrack(one.Probe.Tags, one.Probe.Measured, one.File.RelPath)
		recordings, confidence := c.recordingsFor(ctx, file, local)
		home, recording, found := homeGroup(recordings, local, hints, file.refuted)
		if !found {
			out.note(file.index, library.AttemptPass{Pass: PassTrack, Outcome: PassOutcomeNone})
			pending = append(pending, file)
			continue
		}
		candidate := model.Identification{
			Strategy:   StrategyTrack,
			ExternalID: &model.ExternalID{Provider: ProviderReleaseGroup, ExternalID: home.MBID, Confidence: confidence},
			Precedence: model.PrecedenceSearched,
			Confidence: confidence,
			Evidence: []model.MatchEvidence{
				{Kind: EvidenceTitle, Detail: home.Title},
				{Kind: EvidenceReleaseType, Detail: home.PrimaryType},
				{Kind: EvidenceRecording, Detail: recording.RecordingMBID},
			},
		}
		out.note(file.index, library.AttemptPass{
			Pass: PassTrack, Outcome: PassOutcomeIdentified,
			Candidates: library.AttemptCandidatesFrom([]model.Identification{candidate}),
		})
		out.identify(file.index, library.WorkLink{
			Provider: ProviderReleaseGroup, ExternalID: home.MBID, Confidence: confidence,
			Work:         library.WorkIdentity{MediaType: "music", CanonicalTitle: home.Title, SortTitle: SortTitle(home.Title)},
			RootFolderID: out.rootFolderID, MediaObjectID: out.mediaObjectIDs[file.index],
		}, TrackFile{MediaObjectID: out.mediaObjectIDs[file.index], Title: local.Title.Value, RecordingMBID: recording.RecordingMBID})
	}
	return pending
}

// recordingsFor answers what recording the file is: the one its fingerprint or embedded id names,
// else the recordings a search finds by its artist and title at its length.
func (c Consumer) recordingsFor(ctx context.Context, file pendingFile, local LocalTrack) ([]musicbrainz.RecordingMatch, float64) {
	for _, mbid := range []string{file.recording, local.RecordingMBID} {
		if mbid == "" {
			continue
		}
		recording, err := c.tracks.Recording(ctx, musicbrainz.PriorityIdentify, mbid)
		if err == nil && len(recording.ReleaseGroups) > 0 {
			return []musicbrainz.RecordingMatch{recording}, trackKnownConfidence
		}
		if err != nil && !errors.Is(err, musicbrainz.ErrNotFound) {
			return nil, 0
		}
	}
	if !local.Title.present() || !local.Artist.present() || local.Duration <= 0 {
		return nil, 0
	}
	found, err := c.tracks.SearchRecordings(ctx, musicbrainz.PriorityIdentify, local.Artist.Value, local.Title.Value, trackSearchLimit)
	if err != nil {
		return nil, 0
	}
	var fitting []musicbrainz.RecordingMatch
	for _, recording := range found {
		length := time.Duration(recording.DurationMs) * time.Millisecond
		if recording.DurationMs > 0 && (length-local.Duration).Abs() <= trackLengthTolerance &&
			match.EditPenalty(local.Title.Value, recording.Title) <= trackTitlePenalty &&
			artistPenalty(local.Artist.Value, recording.ArtistCredit) <= maxArtistPenalty {
			fitting = append(fitting, recording)
		}
	}
	return fitting, trackSearchedConfidence
}

// homeGroup chooses the album among every release group the recordings are on, never one the album
// pass already found no track for this file on, and answers the recording that is on it.
func homeGroup(
	recordings []musicbrainz.RecordingMatch, local LocalTrack, hints, refuted []string,
) (musicbrainz.ReleaseGroup, musicbrainz.RecordingMatch, bool) {
	type option struct {
		group     musicbrainz.ReleaseGroup
		recording musicbrainz.RecordingMatch
	}
	named := []string{local.Album.Value, cleanAlbumTag(local.Album.Value), local.FolderAlbum}
	hinted := func(group musicbrainz.ReleaseGroup) int {
		if slices.Contains(hints, group.MBID) || slices.ContainsFunc(named, func(title string) bool {
			return title != "" && match.EditPenalty(title, group.Title) <= distanceEpsilon
		}) {
			return 0
		}
		return 1
	}
	var options []option
	for _, recording := range recordings {
		for _, group := range recording.ReleaseGroups {
			if !slices.Contains(refuted, group.MBID) {
				options = append(options, option{group: group, recording: recording})
			}
		}
	}
	if len(options) == 0 {
		return musicbrainz.ReleaseGroup{}, musicbrainz.RecordingMatch{}, false
	}
	best := slices.MinFunc(options, func(a, b option) int {
		return cmp.Or(
			cmp.Compare(hinted(a.group), hinted(b.group)),
			cmp.Compare(releaseTypePenalty(a.group), releaseTypePenalty(b.group)),
			compareDates(a.group.FirstReleaseDate, b.group.FirstReleaseDate),
			strings.Compare(a.group.MBID, b.group.MBID),
		)
	})
	return best.group, best.recording, true
}

// compareDates orders partial dates earliest first, an unknown date last.
func compareDates(a, b string) int {
	switch {
	case a == b:
		return 0
	case a == "":
		return 1
	case b == "":
		return -1
	}
	return strings.Compare(a, b)
}
