package music

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/importing/inspect"
	"github.com/makearr/MediaNexus/internal/platform/mediatool"
)

// BuildInspectTagReader probes every one of mediaRelPaths under root in one
// batched call (ADR-28 -- a subprocess per file is what the batch interface
// exists to avoid) and returns an inspect.TagReader closed over the result, so
// inspect.Run's own per-file signature never has to know a probe happened.
func BuildInspectTagReader(
	ctx context.Context, tool mediatool.Tool, root string, mediaRelPaths []string,
) (inspect.TagReader, error) {
	absolute := make([]string, len(mediaRelPaths))
	for i, rel := range mediaRelPaths {
		absolute[i] = filepath.Join(root, rel)
	}

	probes, err := tool.ReadTags(ctx, absolute)
	if err != nil {
		return nil, fmt.Errorf("music: inspect tag reader: %w", err)
	}
	if len(probes) != len(mediaRelPaths) {
		return nil, fmt.Errorf("music: inspect tag reader: got %d probes for %d paths",
			len(probes), len(mediaRelPaths))
	}

	byRelPath := make(map[string]inspect.Tags, len(probes))
	for i, probe := range probes {
		if probe.Err != nil {
			// Not found, not empty: a file ReadTags could not open at all
			// (corrupt, not actually audio) carries no evidence either way.
			continue
		}
		byRelPath[mediaRelPaths[i]] = tagsFromProbe(probe.Tags)
	}

	return func(relPath string) inspect.Tags {
		return byRelPath[relPath]
	}, nil
}

// tagsFromProbe reads the same tag names identifier.go and search_identifier.go
// already read (MUSICBRAINZ_ALBUMID, ALBUMARTIST/ARTIST, ALBUM), so a file that
// agrees with the ADR-24 chain's own claims also agrees for inspection.
func tagsFromProbe(tags mediatool.Tags) inspect.Tags {
	artist := tags.First("albumartist", "album_artist", "artist")
	return inspect.Tags{
		Found:       true,
		Artist:      artist,
		Album:       tags.First("album"),
		AlbumArtist: artist,
		ReleaseID:   tags.First("musicbrainz_albumid"),
		DiscNumber:  leadingInt(tags.First("discnumber", "disc")),
		TrackNumber: leadingInt(tags.First("tracknumber", "track")),
	}
}

// leadingInt reads a tag value that may be "N" or "N/total" (TRACKNUMBER's two
// common shapes), treating anything unparseable as absent.
func leadingInt(value string) int {
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		value = value[:slash]
	}
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return n
}
