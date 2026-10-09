package library

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Duplicates finds tracks a library holds more than once, by what they are (artist, title, length) and not by their
// bytes: a flac and a 96 kbit mp3 of one song are one track twice. It only reads.
type Duplicates struct{ store *store.Store }

// NewDuplicates builds it over an open library store.
func NewDuplicates(opened *store.Store) Duplicates { return Duplicates{store: opened} }

// DuplicatePath is one place a copy lives.
type DuplicatePath struct {
	RootFolderID int64
	RelPath      string
}

// DuplicateCopy is one stored file of the track, with what it is: the album it sits on and its measured quality.
type DuplicateCopy struct {
	MediaObjectID  int64
	WorkID         int64
	Album          string
	SizeBytes      int64
	DurationMs     int64
	BitrateKbps    int64
	AudioCodec     string
	Container      string
	Lossless       bool
	BitDepth, Rate int64
	Paths          []DuplicatePath
}

// DuplicateGroup is one track stored more than once.
type DuplicateGroup struct {
	Artist, Title string
	Copies        []DuplicateCopy
}

// durationToleranceMs is how far apart two lengths may be for one recording: encoders and trimmed silence move it.
const durationToleranceMs = 3000

// Groups answers the parts held more than once in one library, those with the most bytes in their extra copies first.
// byName matches one recording under any work by its artist, title and length (music); otherwise copies are matched by
// the part record they cover, never by a name. Only files the probe has measured a length for are compared.
func (d Duplicates) Groups(ctx context.Context, categoryID int64, rootFolderIDs []int64, limit int, byName bool) ([]DuplicateGroup, error) {
	inRoots := make(map[int64]bool, len(rootFolderIDs))
	for _, id := range rootFolderIDs {
		inRoots[id] = true
	}
	rows, err := librarydb.New(d.store.Read()).ListTrackFilesWithMetadata(ctx, categoryID)
	if err != nil {
		return nil, fmt.Errorf("library: duplicate tracks: %w", err)
	}
	type key struct {
		artist, title string
		partID        int64
	}
	byTrack := map[key][]*DuplicateCopy{}
	titles := map[key][2]string{}
	copyOf := map[int64]*DuplicateCopy{}
	for _, row := range rows {
		if !inRoots[row.RootFolderID] || row.DurationMs <= 0 {
			continue
		}
		k := key{artist: normaliseTrackText(row.Artist), title: normaliseTrackText(row.TrackTitle)}
		if !byName {
			k = key{partID: row.PartID}
		} else if k.title == "" {
			continue
		}
		path := DuplicatePath{RootFolderID: row.RootFolderID, RelPath: row.RelPath}
		if existing, seen := copyOf[row.MediaObjectID]; seen {
			existing.Paths = append(existing.Paths, path)
			continue
		}
		stored := &DuplicateCopy{
			MediaObjectID: row.MediaObjectID, WorkID: row.WorkID, Album: row.AlbumTitle, SizeBytes: row.SizeBytes,
			DurationMs: row.DurationMs, BitrateKbps: row.Bitrate / 1000, AudioCodec: row.AudioCodec, Container: row.Container,
			Lossless: row.Lossless == 1, BitDepth: row.BitDepth, Rate: row.SampleRate, Paths: []DuplicatePath{path},
		}
		copyOf[row.MediaObjectID] = stored
		byTrack[k] = append(byTrack[k], stored)
		// The plainest spelling names the group: "Song" rather than "Song (Radio Edit)".
		if named, seen := titles[k]; !seen || len(row.TrackTitle) < len(named[1]) {
			titles[k] = [2]string{row.Artist, row.TrackTitle}
		}
	}
	var groups []DuplicateGroup
	for k, copies := range byTrack {
		if len(copies) < 2 {
			continue
		}
		slices.SortFunc(copies, func(a, b *DuplicateCopy) int { return int(a.DurationMs - b.DurationMs) })
		if k.partID != 0 {
			group := DuplicateGroup{Artist: titles[k][0], Title: titles[k][1]}
			for _, one := range copies {
				group.Copies = append(group.Copies, *one)
			}
			groups = append(groups, group)
			continue
		}
		start := 0
		for index := 1; index <= len(copies); index++ {
			if index < len(copies) && copies[index].DurationMs-copies[index-1].DurationMs <= durationToleranceMs {
				continue
			}
			if index-start >= 2 {
				group := DuplicateGroup{Artist: titles[k][0], Title: titles[k][1]}
				for _, one := range copies[start:index] {
					group.Copies = append(group.Copies, *one)
				}
				groups = append(groups, group)
			}
			start = index
		}
	}
	slices.SortFunc(groups, func(a, b DuplicateGroup) int {
		if diff := b.ExtraBytes() - a.ExtraBytes(); diff != 0 {
			return int(diff)
		}
		return strings.Compare(a.Title, b.Title)
	})
	if limit > 0 && len(groups) > limit {
		groups = groups[:limit]
	}
	return groups, nil
}

// ExtraBytes is what every copy but the largest holds, an upper bound on what dropping the extras would free.
func (g DuplicateGroup) ExtraBytes() int64 {
	var total, largest int64
	for _, one := range g.Copies {
		total += one.SizeBytes
		largest = max(largest, one.SizeBytes)
	}
	return total - largest
}

// normaliseTrackText folds a title or artist to what identifies it: lower case, bracketed notes ("(Remastered 2011)",
// "[Live]") dropped, everything but letters and digits collapsed.
func normaliseTrackText(text string) string {
	var out strings.Builder
	depth := 0
	for _, r := range strings.ToLower(text) {
		switch {
		case r == '(' || r == '[':
			depth++
		case r == ')' || r == ']':
			depth = max(depth-1, 0)
		case depth > 0:
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			out.WriteRune(r)
		default:
			out.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}
