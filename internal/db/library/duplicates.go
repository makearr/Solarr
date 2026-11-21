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
	if byName {
		loose, err := librarydb.New(d.store.Read()).ListUnattachedAudioFiles(ctx, categoryID)
		if err != nil {
			return nil, fmt.Errorf("library: duplicate loose files: %w", err)
		}
		return d.groupsByRecording(rows, loose, inRoots, limit), nil
	}
	type key struct {
		partID int64
		// Different editions of a work are never one another's duplicates (Invariant 10).
		editionID int64
	}
	byTrack := map[key][]*DuplicateCopy{}
	titles := map[key][2]string{}
	copyOf := map[int64]*DuplicateCopy{}
	for _, row := range rows {
		if !inRoots[row.RootFolderID] || row.DurationMs <= 0 {
			continue
		}
		k := key{partID: row.PartID, editionID: row.EditionID}
		path := DuplicatePath{RootFolderID: row.RootFolderID, RelPath: row.RelPath}
		if existing, seen := copyOf[row.MediaObjectID]; seen {
			existing.Paths = append(existing.Paths, path)
			continue
		}
		stored := duplicateCopyOf(row, path)
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
		group := DuplicateGroup{Artist: titles[k][0], Title: titles[k][1]}
		for _, one := range copies {
			group.Copies = append(group.Copies, *one)
		}
		groups = append(groups, group)
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

func duplicateCopyOf(row librarydb.ListTrackFilesWithMetadataRow, path DuplicatePath) *DuplicateCopy {
	return &DuplicateCopy{
		MediaObjectID: row.MediaObjectID, WorkID: row.WorkID, Album: row.AlbumTitle, SizeBytes: row.SizeBytes,
		DurationMs: row.DurationMs, BitrateKbps: row.Bitrate / 1000, AudioCodec: row.AudioCodec, Container: row.Container,
		Lossless: row.Lossless == 1, BitDepth: row.BitDepth, Rate: row.SampleRate, Paths: []DuplicatePath{path},
	}
}

// groupsByRecording finds one recording stored more than once, whatever album or edition each copy sits on: two files
// within a few seconds of one length are one recording when their audio fingerprints match, and, for a file with no
// fingerprint yet, when their artist and title do. Fingerprints decide whenever both files have one.
func (d Duplicates) groupsByRecording(
	rows []librarydb.ListTrackFilesWithMetadataRow, loose []librarydb.ListUnattachedAudioFilesRow, inRoots map[int64]bool, limit int,
) []DuplicateGroup {
	type candidate struct {
		copy          *DuplicateCopy
		artist, title string
		spelling      [2]string
		print         []uint32
	}
	var found []*candidate
	byObject := map[int64]*candidate{}
	for _, row := range rows {
		if !inRoots[row.RootFolderID] || row.DurationMs <= 0 {
			continue
		}
		path := DuplicatePath{RootFolderID: row.RootFolderID, RelPath: row.RelPath}
		if existing, seen := byObject[row.MediaObjectID]; seen {
			existing.copy.Paths = append(existing.copy.Paths, path)
			continue
		}
		one := &candidate{
			copy: duplicateCopyOf(row, path), artist: normaliseTrackText(row.Artist), title: normaliseTrackText(row.TrackTitle),
			spelling: [2]string{row.Artist, row.TrackTitle}, print: DecodeFingerprint(row.AudioFp),
		}
		byObject[row.MediaObjectID] = one
		found = append(found, one)
	}
	// A file covering no track has no title: it joins a group only through its fingerprint.
	for _, row := range loose {
		if !inRoots[row.RootFolderID] || row.DurationMs <= 0 {
			continue
		}
		path := DuplicatePath{RootFolderID: row.RootFolderID, RelPath: row.RelPath}
		if existing, seen := byObject[row.MediaObjectID]; seen {
			existing.copy.Paths = append(existing.copy.Paths, path)
			continue
		}
		one := &candidate{
			copy: &DuplicateCopy{
				MediaObjectID: row.MediaObjectID, WorkID: row.WorkID, Album: row.AlbumTitle, SizeBytes: row.SizeBytes,
				DurationMs: row.DurationMs, BitrateKbps: row.Bitrate / 1000, AudioCodec: row.AudioCodec, Container: row.Container,
				Lossless: row.Lossless == 1, BitDepth: row.BitDepth, Rate: row.SampleRate, Paths: []DuplicatePath{path},
			},
			print: DecodeFingerprint(row.AudioFp),
		}
		byObject[row.MediaObjectID] = one
		found = append(found, one)
	}
	slices.SortFunc(found, func(a, b *candidate) int { return int(a.copy.DurationMs - b.copy.DurationMs) })
	parent := make([]int, len(found))
	for index := range parent {
		parent[index] = index
	}
	root := func(index int) int {
		for parent[index] != index {
			parent[index] = parent[parent[index]]
			index = parent[index]
		}
		return index
	}
	for first := range found {
		for second := first + 1; second < len(found) && found[second].copy.DurationMs-found[first].copy.DurationMs <= durationToleranceMs; second++ {
			a, b := found[first], found[second]
			var same bool
			if len(a.print) > 0 && len(b.print) > 0 {
				same = SameRecording(a.print, b.print)
			} else {
				same = a.title != "" && a.title == b.title && a.artist == b.artist
			}
			if same {
				parent[root(first)] = root(second)
			}
		}
	}
	members := map[int][]*candidate{}
	for index, one := range found {
		members[root(index)] = append(members[root(index)], one)
	}
	var groups []DuplicateGroup
	for _, set := range members {
		if len(set) < 2 {
			continue
		}
		// The plainest spelling names the group: "Song" rather than "Song (Radio Edit)".
		var named [2]string
		group := DuplicateGroup{}
		for _, one := range set {
			if one.spelling[1] != "" && (named[1] == "" || len(one.spelling[1]) < len(named[1])) {
				named = one.spelling
			}
			group.Copies = append(group.Copies, *one.copy)
		}
		group.Artist, group.Title = named[0], named[1]
		if group.Title == "" {
			group.Title = group.Copies[0].Album
		}
		groups = append(groups, group)
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
	return groups
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

// normaliseTrackText folds a title or artist to what identifies it: lower case, everything but letters and digits
// collapsed, and bracketed notes dropped ("(Remastered 2011)") unless they name another recording ("(Dub Mix)").
func normaliseTrackText(text string) string {
	var out, note strings.Builder
	depth := 0
	for _, r := range strings.ToLower(text) {
		switch {
		case r == '(' || r == '[':
			if depth == 0 {
				note.Reset()
			}
			depth++
		case r == ')' || r == ']':
			depth = max(depth-1, 0)
			if depth == 0 && namesAnotherRecording(note.String()) {
				out.WriteString(" " + note.String() + " ")
			}
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			if depth > 0 {
				note.WriteRune(' ')
			} else {
				out.WriteRune(' ')
			}
		case depth > 0:
			note.WriteRune(r)
		default:
			out.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(out.String()), " ")
}

// recordingWords mark a bracketed note as another recording of the song, not another release of the same one.
var recordingWords = []string{
	"mix", "remix", "dub", "instrumental", "live", "acoustic", "demo", "cappella", "karaoke", "extended", "reprise", "unplugged",
}

func namesAnotherRecording(note string) bool {
	for _, word := range strings.Fields(note) {
		if slices.Contains(recordingWords, word) {
			return true
		}
	}
	return false
}
