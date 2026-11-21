package adapters

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
)

// Playback satisfies apiserver.PlaybackLocator over the library's paths and measured facts.
type Playback struct {
	Physical library.Physical
	Cache    *settings.Cache
}

// WorksOfFile answers the works a file belongs to.
func (a Playback) WorksOfFile(ctx context.Context, mediaObjectID int64) ([]int64, error) {
	return a.Physical.WorksOfFile(ctx, mediaObjectID)
}

// LocatePlayback answers every path of the file that is not missing and whose root folder is still configured.
func (a Playback) LocatePlayback(ctx context.Context, mediaObjectID int64) ([]apiserver.PlaybackFile, error) {
	facts, err := a.Physical.PlaybackFacts(ctx, mediaObjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, apiserver.ErrNoSuchFile
	}
	if err != nil {
		return nil, err
	}
	paths, err := a.Physical.PathsForObject(ctx, mediaObjectID)
	if err != nil {
		return nil, err
	}
	snapshot := a.Cache.Snapshot()
	var files []apiserver.PlaybackFile
	for _, candidate := range paths {
		if candidate.MissingSince.Valid {
			continue
		}
		root, found := snapshot.RootFolder(candidate.RootFolderID)
		if !found {
			continue
		}
		files = append(files, apiserver.PlaybackFile{
			MediaObjectID: mediaObjectID, AbsolutePath: filepath.Join(root.Path, candidate.RelPath),
			RootFolderID: candidate.RootFolderID, Kind: facts.Kind, DurationMs: facts.DurationMs,
			Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec,
			Width: facts.Width, Height: facts.Height, OverallKbps: facts.OverallBitrate / 1000, VideoKbps: facts.VideoBitrate / 1000,
		})
	}
	if len(files) == 0 {
		return nil, apiserver.ErrNoSuchFile
	}
	return files, nil
}
