package music

import (
	"cmp"
	"maps"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/identify/match"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// titleMatchThreshold is the minimum title similarity Align accepts without a recording MBID or position; below it a candidate stays unmatched (section 19.6).
const titleMatchThreshold = 0.75

// TrackFile is one candidate audio file's own claimed position and identity,
// read from tags and its path before Align runs -- Align itself does no I/O, the same
// separation internal/moving/placement keeps from internal/jobs/capacity.
type TrackFile struct {
	MediaObjectID int64
	RelPath       string
	Disc          int
	Track         int
	// Side and SideTrack are a vinyl position ("B2"), which no disc or track number says.
	Side          int
	SideTrack     int
	Title         string
	RecordingMBID string
	// DiscFrom and TrackFrom say where the value came from: "tag", "folder" or "name"; empty when unknown.
	DiscFrom  string
	TrackFrom string
}

const (
	fromTag    = "tag"
	fromFolder = "folder"
	fromName   = "name"
)

var (
	nameDiscTrackRe = regexp.MustCompile(`^(\d{1,2})[-.](\d{1,3})\b`)
	nameTrackRe     = regexp.MustCompile(`^(\d{1,3})\b`)
	vinylRe         = regexp.MustCompile(`^([A-Ha-h])(\d{1,2})\b`)
	titleLeadRe     = regexp.MustCompile(`^[\s._-]+`)
)

// ReadTrackFile pulls a candidate's disc/track/title/recording id out of its
// tags, the same "no network call" discipline as ReadMBIDs. What the tags leave empty is read from the path:
// the disc from its folder, the position and title from the file name.
func ReadTrackFile(mediaObjectID int64, relPath string, tags mediatool.Tags) TrackFile {
	file := TrackFile{
		MediaObjectID: mediaObjectID,
		RelPath:       relPath,
		Disc:          firstTrackNumber(tags.First("discnumber", "disc")),
		Track:         firstTrackNumber(tags.First("tracknumber", "track")),
		Title:         tags.First("title"),
		RecordingMBID: ReadMBIDs(tags).Recording,
	}
	if file.Disc > 0 {
		file.DiscFrom = fromTag
	}
	if file.Track > 0 {
		file.TrackFrom = fromTag
	} else if side, sideTrack, ok := vinylPosition(tags.First("tracknumber", "track")); ok {
		file.Side, file.SideTrack, file.TrackFrom = side, sideTrack, fromTag
	}
	if relPath == "" {
		return file
	}
	if file.Disc == 0 {
		if disc, ok := inspect.DiscNumber(path.Base(path.Dir(relPath))); ok {
			file.Disc, file.DiscFrom = disc, fromFolder
		}
	}
	stem := strings.TrimSuffix(path.Base(relPath), path.Ext(relPath))
	var rest string
	switch {
	case nameDiscTrackRe.MatchString(stem):
		found := nameDiscTrackRe.FindStringSubmatch(stem)
		disc, _ := strconv.Atoi(found[1])
		track, _ := strconv.Atoi(found[2])
		if file.Disc == 0 && disc > 0 {
			file.Disc, file.DiscFrom = disc, fromName
		}
		if file.TrackFrom == "" {
			file.Track, file.TrackFrom = track, fromName
		}
		rest = stem[len(found[0]):]
	case nameTrackRe.MatchString(stem):
		found := nameTrackRe.FindStringSubmatch(stem)
		if file.TrackFrom == "" {
			file.Track, file.TrackFrom = firstTrackNumber(found[1]), fromName
		}
		rest = stem[len(found[0]):]
	case vinylRe.MatchString(stem):
		found := vinylRe.FindStringSubmatch(stem)
		if file.TrackFrom == "" {
			file.Side, file.SideTrack, file.TrackFrom = int(strings.ToLower(found[1])[0]-'a')+1, firstTrackNumber(found[2]), fromName
		}
		rest = stem[len(found[0]):]
	}
	if file.Title == "" {
		file.Title = strings.TrimSpace(titleLeadRe.ReplaceAllString(rest, ""))
	}
	return file
}

// vinylPosition reads "A1" or "b12" from a track number tag.
func vinylPosition(value string) (side, track int, ok bool) {
	found := vinylRe.FindStringSubmatch(strings.TrimSpace(value))
	if found == nil {
		return 0, 0, false
	}
	return int(strings.ToLower(found[1])[0]-'a') + 1, firstTrackNumber(found[2]), true
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

// trackTotal is the total of a "3/10" track number, else the separate total tag, else 0.
func trackTotal(number, total string) int {
	if _, after, found := strings.Cut(number, "/"); found {
		return firstTrackNumber(after)
	}
	return firstTrackNumber(total)
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

// Align matches files to the tracklist in three passes: recording id (1.0), disc and track position (0.9), then title similarity above titleMatchThreshold, which tolerates a bonus track shifting positions.
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

	discs := 1
	order := make([]int, 0, len(parts))
	for index, part := range parts {
		if disc, _, err := parseMediaRef(part.MediaRef); err == nil {
			discs = max(discs, disc)
			order = append(order, index)
		}
	}
	slices.SortStableFunc(order, func(a, b int) int {
		discA, trackA, _ := parseMediaRef(parts[a].MediaRef)
		discB, trackB, _ := parseMediaRef(parts[b].MediaRef)
		return cmp.Or(cmp.Compare(discA, discB), cmp.Compare(trackA, trackB))
	})

	// A bonus track can land exactly on a later track's expected position, shifting it without replacing it
	// (section 19.6), so a position is accepted only when titles on both sides agree, or one side has none.
	agrees := func(part Part, candidate TrackFile) bool {
		return part.Title == "" || candidate.Title == "" || match.TitleSimilarity(part.Title, candidate.Title) >= titleMatchThreshold
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
			candidateDisc, candidateTrack := candidate.Disc, candidate.Track
			if candidateDisc == 0 {
				switch {
				case discs == 1:
					candidateDisc = 1
				case candidate.TrackFrom == fromName && candidateTrack >= 100:
					// "203 Title" is the third track of disc 2.
					candidateDisc, candidateTrack = candidateTrack/100, candidateTrack%100
				default:
					continue
				}
			}
			if (candidateTrack == 0 && (candidate.TrackFrom == "" || candidate.Side > 0)) || candidateDisc != disc || candidateTrack != track || !agrees(part, candidate) {
				continue
			}
			file := take(candidateIndex)
			result.Parts[index].File = &file
			result.Parts[index].Confidence = 0.9
			result.Parts[index].Basis = "position"
			break
		}
	}

	// Files with no known disc on a multi-disc album, or on vinyl: the Nth track of the release in disc order.
	sideOffset := map[int]int{}
	sideLength := map[int]int{}
	for _, candidate := range remaining {
		if candidate.Disc == 0 && candidate.Side > 0 {
			sideLength[candidate.Side] = max(sideLength[candidate.Side], candidate.SideTrack)
		}
	}
	offset := 0
	for _, side := range slices.Sorted(maps.Keys(sideLength)) {
		sideOffset[side] = offset
		offset += sideLength[side]
	}
	sequenced := map[int]bool{}
	for candidateIndex, candidate := range remaining {
		if candidate.Disc != 0 {
			continue
		}
		position := candidate.Track
		if candidate.Side > 0 {
			position = sideOffset[candidate.Side] + candidate.SideTrack
		}
		if position < 1 || position > len(order) {
			continue
		}
		index := order[position-1]
		if result.Parts[index].File != nil || candidate.Title == "" || !agrees(parts[index], candidate) {
			continue
		}
		sequenced[candidateIndex] = true
		file := candidate
		result.Parts[index].File = &file
		result.Parts[index].Confidence = 0.8
		result.Parts[index].Basis = "sequence"
	}
	if len(sequenced) > 0 {
		var kept []TrackFile
		for candidateIndex, candidate := range remaining {
			if !sequenced[candidateIndex] {
				kept = append(kept, candidate)
			}
		}
		remaining = kept
	}

	// Every (part, file) pair above the threshold, best first, so one close title cannot take a file a closer part wants.
	type pair struct {
		part, candidate int
		score           float64
	}
	var pairs []pair
	for index, part := range parts {
		if result.Parts[index].File != nil || part.Title == "" {
			continue
		}
		for candidateIndex, candidate := range remaining {
			if candidate.Title == "" {
				continue
			}
			if score := match.TitleSimilarity(part.Title, candidate.Title); score >= titleMatchThreshold {
				pairs = append(pairs, pair{index, candidateIndex, score})
			}
		}
	}
	slices.SortStableFunc(pairs, func(a, b pair) int {
		return cmp.Or(-cmp.Compare(a.score, b.score), cmp.Compare(a.part, b.part), cmp.Compare(a.candidate, b.candidate))
	})
	usedCandidate := map[int]bool{}
	for _, each := range pairs {
		if result.Parts[each.part].File != nil || usedCandidate[each.candidate] {
			continue
		}
		file := remaining[each.candidate]
		usedCandidate[each.candidate] = true
		result.Parts[each.part].File = &file
		result.Parts[each.part].Confidence = each.score
		result.Parts[each.part].Basis = "title"
	}
	var left []TrackFile
	for index, candidate := range remaining {
		if !usedCandidate[index] {
			left = append(left, candidate)
		}
	}
	remaining = left

	result.Extra = remaining
	return result
}
