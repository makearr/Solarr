package music

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

// BuildInspectTagGrouper probes every one of mediaRelPaths under root in one batched call (ADR-28) and returns an
// inspect.TagGrouper over the result that groups by the rule scan uses (GroupByAlbumTags).
func BuildInspectTagGrouper(
	ctx context.Context, tool mediatool.Tool, root string, mediaRelPaths []string,
) (inspect.TagGrouper, error) {
	absolute := make([]string, len(mediaRelPaths))
	for i, rel := range mediaRelPaths {
		absolute[i] = filepath.Join(root, rel)
	}

	probes, err := tool.ReadTags(ctx, absolute)
	if err != nil {
		return nil, fmt.Errorf("music: inspect tag grouper: %w", err)
	}
	if len(probes) != len(mediaRelPaths) {
		return nil, fmt.Errorf("music: inspect tag grouper: got %d probes for %d paths",
			len(probes), len(mediaRelPaths))
	}

	byRelPath := make(map[string]AlbumTags, len(probes))
	for i, probe := range probes {
		// A file ReadTags could not open (corrupt, not audio) carries no evidence: it stays untagged.
		if probe.Err == nil {
			byRelPath[mediaRelPaths[i]] = AlbumTagsOf(probe.Tags)
		}
	}

	return func(relPaths []string) (inspect.TagGroups, bool) {
		tags := make([]AlbumTags, len(relPaths))
		for i, relPath := range relPaths {
			tags[i] = byRelPath[relPath]
		}
		return inspectTagGroups(tags)
	}, nil
}

// inspectTagGroups is GroupByAlbumTags with its evidence; false when no file names an album, so inspect falls
// through to the structural signal instead of calling one untagged group agreement.
func inspectTagGroups(tags []AlbumTags) (inspect.TagGroups, bool) {
	if !slices.ContainsFunc(tags, AlbumTags.hasAlbumEvidence) {
		return inspect.TagGroups{}, false
	}
	grouped := inspect.TagGroups{Groups: GroupByAlbumTags(tags)}
	for _, members := range grouped.Groups {
		signal := albumGroupSignal(tags, members)
		grouped.Ambiguous = grouped.Ambiguous || signal.Kind == inspect.SignalDirectoryBoundary
		grouped.Signals = append(grouped.Signals, signal)
	}
	return grouped, true
}

// albumGroupSignal is the strongest evidence a group's files carry: a release id, an album title, or none (the
// untagged files left beside several albums).
func albumGroupSignal(tags []AlbumTags, members []int) inspect.Signal {
	for _, i := range members {
		if tags[i].ReleaseID != "" {
			return inspect.Signal{Kind: inspect.SignalEmbeddedID, Detail: tags[i].ReleaseID}
		}
	}
	for _, i := range members {
		if title := albumTitleKey(tags[i].Album); title != "" {
			artist := foldTag(cmp.Or(tags[i].AlbumArtist, tags[i].Artist))
			return inspect.Signal{Kind: inspect.SignalTagAgreement, Detail: artist + " / " + title}
		}
	}
	return inspect.Signal{Kind: inspect.SignalDirectoryBoundary}
}
