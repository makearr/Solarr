// Package dispatch sends a decided release to a download client and holds it when the client is full: every media
// type's, extracted from music for stage 8.
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
)

// ErrDecisionNotFound reports a decision id that does not belong to the
// item it was asked to dispatch from.
var ErrDecisionNotFound = errors.New("dispatch: decision not found for this item")

// ErrDecisionNotAccepted reports a REJECT/DEFER decision -- ADR-57's
// acquire.send only ever fires on an ACCEPT; a rejected or deferred release
// is not something to hand to a download client.
var ErrDecisionNotAccepted = errors.New("dispatch: decision is not ACCEPT")

// ErrContentBlocklisted is a release whose content failed before, found again under another
// listing (section 10.11); refused before the client sees it, and the next best is tried.
var ErrContentBlocklisted = errors.New("dispatch: the same content failed before and is blocklisted")

// ErrNoDownloaderForProtocol reports no enabled downloader matching the
// candidate's own protocol -- a usenet release with only a torrent client
// configured, for instance.
var ErrNoDownloaderForProtocol = errors.New("dispatch: no enabled downloader for this candidate's protocol")

// DownloaderConn is one configured, enabled download client ready to
// dispatch to -- building the client (credentials, protocol) is the
// caller's own job, the same split IndexerConn already keeps for indexers.
type DownloaderConn struct {
	ID       int64
	Protocol string // "usenet" | "torrent" | "ddl" (ADR-46) -- only usenet has a sender today
	Client   download.Client
	// Category is the client category the media type is sent under.
	Category string
	// MaxQueued is ADR-57's queue depth: an automatic send beyond it is held. 0 means no limit.
	MaxQueued int
	// AlwaysSendUser and AlwaysSendRSS let those grabs past a full queue (2026-09-29).
	AlwaysSendUser bool
	AlwaysSendRSS  bool
}

// GrabOrigin is who asked for a grab; it decides whether a full client holds it and in what order.
type GrabOrigin string

// The origins, in the order held releases are sent (2026-09-29: user > RSS > new > backfill).
const (
	// GrabUser is a person's own ask: an album's Search, an interactive pick, a request.
	GrabUser GrabOrigin = "user"
	// GrabRSS is a release an RSS sync found for a wanted album.
	GrabRSS GrabOrigin = "rss"
	// GrabAutomatic is Solarr searching on its own, or Wanted's Search all.
	GrabAutomatic GrabOrigin = "automatic"
)

// alwaysSent reports whether downloader takes a grab of origin even when its queue is full.
func (d DownloaderConn) alwaysSent(origin GrabOrigin) bool {
	switch origin {
	case GrabUser:
		return d.AlwaysSendUser
	case GrabRSS:
		return d.AlwaysSendRSS
	default:
		return false
	}
}

// ReleaseFetcher downloads a release's file (an NZB) from the indexer that listed it.
type ReleaseFetcher func(ctx context.Context, indexerID int64, link string) ([]byte, error)

// DownloaderSource resolves the currently configured, enabled downloaders
// fresh on every dispatch -- the same "ask, don't cache" discipline
// IndexerSource already follows.
type DownloaderSource func(ctx context.Context) ([]DownloaderConn, error)

// Dispatch is ADR-57's own acquire.send: given an ACCEPTed decision, submit
// its own release to a matching downloader and record the resulting
// acquisition. No resource gate yet -- every dispatch this pass is a user's
// own explicit action, ADR-12's own "user request: never paused" job class,
// since nothing automatic (stage 6's planner) calls this yet.
type Dispatch struct {
	Candidates   library.Candidates
	Acquisitions library.Acquisitions
	Desired      library.Desired
	Downloaders  DownloaderSource
	Fetch        ReleaseFetcher
	// History is optional: told of every grab, for History and the album's page.
	History func(ctx context.Context, kind download.HistoryKind, acquisition library.Acquisition, detail string)
	// Ready is optional: where an automatic send is held while its client's queue is full.
	Ready *library.ReadyQueue
	// Blocklist is optional: a held release blocklisted meanwhile is dropped, not sent.
	Blocklist *library.BlocklistStore
}

// Send dispatches one decision, previously recorded against itemID by
// Search.SearchItem, to whichever configured downloader matches its own
// protocol.
func (d Dispatch) Send(ctx context.Context, itemID, decisionID int64, at time.Time) (library.Acquisition, error) {
	if _, err := d.Desired.Get(ctx, itemID); err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
	}

	detail, err := d.findDecision(ctx, itemID, decisionID)
	if err != nil {
		return library.Acquisition{}, err
	}
	if detail.Decision.Outcome != "ACCEPT" {
		return library.Acquisition{}, ErrDecisionNotAccepted
	}

	// Before the client is involved: refusing after the send leaves a duplicate in its queue.
	if _, inFlight, err := d.Acquisitions.InFlightForItem(ctx, itemID); err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
	} else if inFlight {
		return library.Acquisition{}, library.ErrAcquisitionAlreadyInFlight
	}

	downloaders, err := d.Downloaders(ctx)
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: downloaders: %w", err)
	}
	downloader, found := firstMatchingProtocol(downloaders, detail.ReleaseCandidate.Protocol)
	if !found {
		return library.Acquisition{}, ErrNoDownloaderForProtocol
	}

	category := downloader.Category
	file, err := d.Fetch(ctx, detail.ReleaseCandidate.IndexerID, detail.ReleaseCandidate.DownloadURL)
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: fetch release: %w", err)
	}
	contentHash := ""
	switch detail.ReleaseCandidate.Protocol {
	case "usenet":
		contentHash = download.NZBContentHash(file)
	case "torrent":
		contentHash = download.TorrentContentHash(file)
	}
	if contentHash != "" && d.Blocklist != nil {
		if blocked, err := d.Blocklist.IsBlocked(ctx, nil, "", contentHash, at.Unix()); err != nil {
			return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
		} else if blocked {
			return library.Acquisition{}, ErrContentBlocklisted
		}
	}
	clientRef, err := downloader.Client.Add(ctx, download.AddRequest{
		Name: detail.ReleaseCandidate.Title, File: file, Category: category,
	})
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
	}

	size := int64(0)
	if detail.ReleaseCandidate.SizeBytes != nil {
		size = *detail.ReleaseCandidate.SizeBytes
	}
	desiredItemID := itemID
	candidateID := detail.ReleaseCandidate.ID
	indexerID := detail.ReleaseCandidate.IndexerID
	created, err := d.Acquisitions.Create(ctx, library.NewAcquisition{
		DesiredItemID: &desiredItemID, CandidateID: &candidateID, IndexerID: &indexerID,
		CandidateGUID: detail.ReleaseCandidate.GUID, CandidateTitle: detail.ReleaseCandidate.Title,
		DownloaderID: downloader.ID, DownloadClientRef: clientRef, ClientCategory: category,
		// section 10.9's own "added time + name + size" client fingerprint --
		// a reference reused for a different download no longer matches this.
		ClientFingerprint: fmt.Sprintf("%d:%s:%d", at.Unix(), detail.ReleaseCandidate.Title, size),
		Protocol:          detail.ReleaseCandidate.Protocol,
		// QUEUED, not DOWNLOADING: acquire.send only confirms the client
		// accepted the job, not that it has started pulling data yet --
		// that distinction needs client polling (section 10.9), not built
		// this pass.
		State:     "QUEUED",
		CreatedAt: at.Unix(), UpdatedAt: at.Unix(), ContentHash: contentHash,
	})
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
	}
	if d.History != nil {
		d.History(ctx, download.HistoryGrabbed, created, "")
	}
	return created, nil
}

func (d Dispatch) findDecision(ctx context.Context, itemID, decisionID int64) (library.DecisionDetail, error) {
	details, err := d.Candidates.ListDecisionsForItem(ctx, itemID, DecisionLookupLimit)
	if err != nil {
		return library.DecisionDetail{}, fmt.Errorf("dispatch: list decisions: %w", err)
	}
	for _, detail := range details {
		if detail.Decision.ID == decisionID {
			return detail, nil
		}
	}
	return library.DecisionDetail{}, ErrDecisionNotFound
}

// DecisionLookupLimit bounds findDecision's own lookup -- generous
// for a manual, low-frequency action; the same ceiling
// adapters.CandidateSearch's own history listing already uses.
const DecisionLookupLimit = 100

func firstMatchingProtocol(downloaders []DownloaderConn, protocol string) (DownloaderConn, bool) {
	for _, downloader := range downloaders {
		if downloader.Protocol == protocol {
			return downloader, true
		}
	}
	return DownloaderConn{}, false
}
