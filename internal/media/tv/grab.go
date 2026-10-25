package tv

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/dispatch"
	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/acquiring/judge"
	"github.com/makearr/Solarr/internal/media/works"
)

// Grab is Sonarr's automatic search, the shared one over the TV Search: search one season, then send its best accepted
// release to a download client, the library's free space as its headroom.
type Grab struct {
	Search   Search
	Dispatch dispatch.Dispatch
}

// GrabResult is one automatic search: what was found and, when something was, what was sent.
type GrabResult = works.GrabResult

func (g Grab) shared() works.Grab {
	automatic := dispatch.Automatic{Dispatch: g.Dispatch, Name: "tv"}
	if settings := g.Search.Settings; settings != nil {
		automatic.FreeSpace = func(categoryID int64) (int64, bool) { return settings.Snapshot().AcquisitionHeadroom(categoryID) }
	}
	return works.Grab{Search: g.Search, Automatic: automatic}
}

// SearchAndSend searches itemID unless a download for it is already on its way, and sends the best accepted release,
// or holds it when origin may wait. Finding nothing acceptable is not an error.
func (g Grab) SearchAndSend(ctx context.Context, itemID int64, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	return g.shared().SearchAndSend(ctx, itemID, conns, origin, at)
}

// SearchEpisodeAndSend is SearchAndSend for one episode of the season; zero is what the season is missing.
func (g Grab) SearchEpisodeAndSend(ctx context.Context, itemID int64, episode int, conns []judge.Conn, origin dispatch.GrabOrigin, at time.Time) (GrabResult, error) {
	return g.shared().SearchPartAndSend(ctx, itemID, episode, conns, origin, at)
}

// JudgeFeedAndSend is the RSS sync's grab: a feed's releases are judged against a wanted season and the best accepted
// one is sent as an RSS grab. Nothing is searched.
func (g Grab) JudgeFeedAndSend(ctx context.Context, itemID int64, conn judge.Conn, raws []indexer.RawItem, at time.Time) (GrabResult, error) {
	return g.shared().JudgeFeedAndSend(ctx, itemID, conn, raws, at)
}
