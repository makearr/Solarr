package dispatch

import (
	"context"
	"slices"
)

// choose answers the download client a release goes to: the one its indexer is bound to when that client is enabled and
// takes the protocol, else the lowest-numbered priority of those that take it, preferring one with room in its queue.
func (d Dispatch) choose(ctx context.Context, downloaders []DownloaderConn, indexerID int64, protocol string) (DownloaderConn, bool, error) {
	var takers []DownloaderConn
	for _, downloader := range downloaders {
		if downloader.Protocol == protocol {
			takers = append(takers, downloader)
		}
	}
	if len(takers) == 0 {
		return DownloaderConn{}, false, nil
	}
	if d.BoundClient != nil {
		if bound := d.BoundClient(ctx, indexerID); bound > 0 {
			if at := slices.IndexFunc(takers, func(one DownloaderConn) bool { return one.ID == bound }); at >= 0 {
				return takers[at], true, nil
			}
		}
	}
	slices.SortStableFunc(takers, func(a, b DownloaderConn) int { return a.Priority - b.Priority })
	for _, one := range takers {
		room, err := d.hasRoom(ctx, one)
		if err != nil {
			return DownloaderConn{}, false, err
		}
		if room {
			return one, true, nil
		}
	}
	return takers[0], true, nil
}
