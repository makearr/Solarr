package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media/music"
)

// Duplicates satisfies apiserver.DuplicateReader: the tracks held twice, and which copy the library's target and
// profiles say to keep.
type Duplicates struct {
	Store    library.Duplicates
	Settings *settings.Cache
}

func (d Duplicates) Duplicates(ctx context.Context, categoryID int64, rootFolderIDs []int64, limit int) ([]apiserver.DuplicateGroupRow, error) {
	groups, err := d.Store.Groups(ctx, categoryID, rootFolderIDs, limit)
	if err != nil {
		return nil, err
	}
	snapshot := d.Settings.Snapshot()
	rows := make([]apiserver.DuplicateGroupRow, len(groups))
	for i, group := range groups {
		row := apiserver.DuplicateGroupRow{Artist: group.Artist, Title: group.Title}
		files := make([]music.KeeperFile, len(group.Copies))
		for j, copy := range group.Copies {
			item := apiserver.DuplicateCopyRow{
				MediaObjectID: copy.MediaObjectID, WorkID: copy.WorkID, Album: copy.Album, SizeBytes: copy.SizeBytes,
				DurationMs: copy.DurationMs, BitrateKbps: copy.BitrateKbps, AudioCodec: copy.AudioCodec, Lossless: copy.Lossless,
			}
			for _, path := range copy.Paths {
				item.Paths = append(item.Paths, apiserver.DuplicatePathRow{RootFolderID: path.RootFolderID, RelPath: path.RelPath})
			}
			row.Copies = append(row.Copies, item)
			files[j] = music.KeeperFile{
				AudioCodec: copy.AudioCodec, Container: copy.Container, Lossless: copy.Lossless, BitrateKbps: copy.BitrateKbps,
				BitDepth: copy.BitDepth, SampleRate: copy.Rate, Path: copy.Paths[0].RelPath,
			}
		}
		row.Keep, row.KeepReason = music.ChooseKeeper(snapshot, categoryID, files)
		rows[i] = row
	}
	return rows, nil
}
