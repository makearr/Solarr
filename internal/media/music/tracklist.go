package music

import (
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/identify/match"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
)

// titleMatchThreshold is the minimum internal/identify/match title similarity Align
// accepts as a match when neither a recording MBID nor a disc/track position
// aligns a candidate to a part -- below it, a candidate is left unmatched
// rather than guessed (section 19.6: alignment carries confidence, never a
// silent wrong answer).
const titleMatchThreshold = 0.75

// TrackFile is one candidate audio file's own claimed position and identity,
// read from tags before Align runs -- Align itself does no I/O, the same
// separation internal/moving/placement keeps from internal/jobs/capacity.
type TrackFile struct {
	MediaObjectID int64
	Disc          int
	Track         int
	Title         string
	RecordingMBID string
}

// ReadTrackFile pulls a candidate's disc/track/title/recording id out of its
// tags, the same "no network call" discipline as ReadMBIDs.
func ReadTrackFile(mediaObjectID int64, tags mediatool.Tags) TrackFile {
	return TrackFile{
		MediaObjectID: mediaObjectID,
		Disc:          firstTrackNumber(tags.First("discnumber", "disc")),
		Track:         firstTrackNumber(tags.First("tracknumber", "track")),
		Title:         tags.First("title"),
		RecordingMBID: ReadMBIDs(tags).Recording,
	}
}

// firstTrackNumber reads a tag written as "5" or "5/12", returning 0 for
// anything it cannot parse -- an unknown position must never match part 0.
func firstTrackNumber(value string) int {
	value, _, _ = strings.Cut(strings.TrimSpace(value), "/")
	number, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || number <= 0 {
		return 0
	}
	return number
}

// Part is one expected track Align matches candidates against -- a media
// type's own shape (ADR-23), decoupled from library.db's row so this package
// never needs to import internal/db/store/gen/librarydb directly.
type Part struct {
	PartID        int64
	MediaRef      string
	Title         string
	RecordingMBID string
}

// AlignedPart is one Part matched, or not, to a candidate file -- never a
// boolean, per section 19.6: a caller needs to know how confidently, and on
// what basis.
type AlignedPart struct {
	Part       Part
	File       *TrackFile
	Confidence float64
	Basis      string
}

// Alignment is one work's expected tracklist matched against the files
// claiming to be it. Extra is every candidate that matched no part -- a bonus
// track, tolerated rather than rejected.
type Alignment struct {
	Parts []AlignedPart
	Extra []TrackFile
}

// Align matches candidate files to a work's expected tracklist, three passes
// in descending confidence: a shared MusicBrainz recording id (1.0,
// unambiguous), disc-and-track position (0.9), then title similarity
// (internal/identify/match's token-set/Jaro-Winkler best-of, so a weak title match
// reports a weak confidence rather than snapping to a fixed value) above
// titleMatchThreshold. A bonus track that shifts every later track's
// numbering is exactly what the title pass tolerates: its own position
// matches nothing, but a real track's title still does even once its position
// has shifted past it.
func Align(parts []Part, candidates []TrackFile) Alignment {
	remaining := append([]TrackFile(nil), candidates...)
	result := Alignment{Parts: make([]AlignedPart, len(parts))}
	for index, part := range parts {
		result.Parts[index] = AlignedPart{Part: part}
	}

	take := func(index int) TrackFile {
		file := remaining[index]
		remaining = append(remaining[:index:index], remaining[index+1:]...)
		return file
	}

	for index, part := range parts {
		if part.RecordingMBID == "" {
			continue
		}
		for candidateIndex, candidate := range remaining {
			if candidate.RecordingMBID != "" && candidate.RecordingMBID == part.RecordingMBID {
				file := take(candidateIndex)
				result.Parts[index].File = &file
				result.Parts[index].Confidence = 1.0
				result.Parts[index].Basis = "recording-mbid"
				break
			}
		}
	}

	for index, part := range parts {
		if result.Parts[index].File != nil {
			continue
		}
		disc, track, err := parseMediaRef(part.MediaRef)
		if err != nil {
			continue
		}
		for candidateIndex, candidate := range remaining {
			if candidate.Track == 0 || candidate.Disc != disc || candidate.Track != track {
				continue
			}
			// A bonus track can land exactly on a later track's expected
			// position, shifting it without replacing it (section 19.6).
			// Positional agreement alone cannot tell those apart, so when
			// both sides carry a title it must at least roughly corroborate
			// the position before this pass accepts it -- otherwise the
			// slot is left for the title pass below to resolve properly.
			if part.Title != "" && candidate.Title != "" &&
				match.TitleSimilarity(part.Title, candidate.Title) < titleMatchThreshold {
				continue
			}
			file := take(candidateIndex)
			result.Parts[index].File = &file
			result.Parts[index].Confidence = 0.9
			result.Parts[index].Basis = "position"
			break
		}
	}

	for index, part := range parts {
		if result.Parts[index].File != nil || part.Title == "" {
			continue
		}
		bestIndex, bestScore := -1, 0.0
		for candidateIndex, candidate := range remaining {
			if candidate.Title == "" {
				continue
			}
			if score := match.TitleSimilarity(part.Title, candidate.Title); score > bestScore {
				bestIndex, bestScore = candidateIndex, score
			}
		}
		if bestIndex >= 0 && bestScore >= titleMatchThreshold {
			file := take(bestIndex)
			result.Parts[index].File = &file
			result.Parts[index].Confidence = bestScore
			result.Parts[index].Basis = "title"
		}
	}

	result.Extra = remaining
	return result
}
