package music

import "github.com/makearr/Solarr/internal/acquiring/dispatch"

// Sending a chosen release to a download client is every media type's, so it lives in internal/acquiring/dispatch;
// these names keep music's callers as they were.
type (
	// Dispatch sends a decided release to a download client.
	Dispatch = dispatch.Dispatch
	// DownloaderConn is one enabled download client ready to dispatch to.
	DownloaderConn = dispatch.DownloaderConn
	// GrabOrigin is who asked for a grab.
	GrabOrigin = dispatch.GrabOrigin
	// ReleaseFetcher downloads a release's NZB or torrent.
	ReleaseFetcher = dispatch.ReleaseFetcher
	// DownloaderSource lists the enabled downloaders.
	DownloaderSource = dispatch.DownloaderSource
)

// The grab origins.
const (
	GrabUser      = dispatch.GrabUser
	GrabRSS       = dispatch.GrabRSS
	GrabAutomatic = dispatch.GrabAutomatic
)

// Errors dispatch reports.
var (
	ErrDecisionNotFound        = dispatch.ErrDecisionNotFound
	ErrDecisionNotAccepted     = dispatch.ErrDecisionNotAccepted
	ErrContentBlocklisted      = dispatch.ErrContentBlocklisted
	ErrNoDownloaderForProtocol = dispatch.ErrNoDownloaderForProtocol
)
