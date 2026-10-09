package adapters

import (
	"context"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media/music"
)

// ArtistActions implements the artist page's bulk verbs over the people store, the works monitor
// and the job queue.
type ArtistActions struct {
	People  library.People
	Monitor WorksMonitor
	Queue   *jobs.Queue
}

// SearchMissing searches the artist's missing albums in each library named; duplicate when every
// one of those searches was already queued.
func (a ArtistActions) SearchMissing(ctx context.Context, personID int64, categoryIDs []int64) (bool, error) {
	if _, err := a.People.Person(ctx, personID); err != nil {
		return false, err
	}
	duplicate := true
	for _, categoryID := range categoryIDs {
		result, err := music.EnqueueSearchArtist(ctx, a.Queue, categoryID, personID)
		if err != nil {
			return false, err
		}
		duplicate = duplicate && result.Duplicate
	}
	return duplicate, nil
}

// Refresh enqueues the artist's refresh.
func (a ArtistActions) Refresh(ctx context.Context, personID int64) (bool, error) {
	if _, err := a.People.Person(ctx, personID); err != nil {
		return false, err
	}
	result, err := music.EnqueueRefreshArtist(ctx, a.Queue, personID)
	if err != nil {
		return false, err
	}
	return result.Duplicate, nil
}

// MonitorAll monitors every album the artist is credited on as artist.
func (a ArtistActions) MonitorAll(ctx context.Context, personID int64) (int64, error) {
	if _, err := a.People.Person(ctx, personID); err != nil {
		return 0, err
	}
	works, err := a.People.Discography(ctx, personID, "artist")
	if err != nil {
		return 0, err
	}
	var monitored int64
	for _, work := range works {
		if _, err := a.Monitor.MonitorWork(ctx, work.ID, apiserver.MonitorScope{Monitored: true}); err != nil {
			return monitored, err
		}
		monitored++
	}
	return monitored, nil
}

// AlbumRefresher enqueues one album's refresh.
type AlbumRefresher struct {
	Works library.Works
	Queue *jobs.Queue
}

// RefreshAlbum enqueues the album's refresh; a second while one runs is folded in.
func (a AlbumRefresher) RefreshAlbum(ctx context.Context, workID int64) (bool, error) {
	if _, err := a.Works.Detail(ctx, workID); err != nil {
		return false, err
	}
	result, err := music.EnqueueRefreshAlbum(ctx, a.Queue, workID)
	if err != nil {
		return false, err
	}
	return result.Duplicate, nil
}
