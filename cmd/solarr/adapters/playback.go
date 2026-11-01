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

// LocatePlayback answers the first path of the file whose root folder is still configured.
func (a Playback) LocatePlayback(ctx context.Context, mediaObjectID int64) (apiserver.PlaybackFile, error) {
	facts, err := a.Physical.PlaybackFacts(ctx, mediaObjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return apiserver.PlaybackFile{}, apiserver.ErrNoSuchFile
	}
	if err != nil {
		return apiserver.PlaybackFile{}, err
	}
	paths, err := a.Physical.PathsForObject(ctx, mediaObjectID)
	if err != nil {
		return apiserver.PlaybackFile{}, err
	}
	snapshot := a.Cache.Snapshot()
	for _, candidate := range paths {
		if candidate.MissingSince.Valid {
			continue
		}
		root, found := snapshot.RootFolder(candidate.RootFolderID)
		if !found {
			continue
		}
		return apiserver.PlaybackFile{
			MediaObjectID: mediaObjectID, AbsolutePath: filepath.Join(root.Path, candidate.RelPath),
			RootFolderID: candidate.RootFolderID, Kind: facts.Kind, DurationMs: facts.DurationMs,
			Container: facts.Container, VideoCodec: facts.VideoCodec, AudioCodec: facts.AudioCodec,
		}, nil
	}
	return apiserver.PlaybackFile{}, apiserver.ErrNoSuchFile
}
