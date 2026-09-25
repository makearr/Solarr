package music

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
var ErrDecisionNotFound = errors.New("music: dispatch: decision not found for this item")

// ErrDecisionNotAccepted reports a REJECT/DEFER decision -- ADR-57's
// acquire.send only ever fires on an ACCEPT; a rejected or deferred release
// is not something to hand to a download client.
var ErrDecisionNotAccepted = errors.New("music: dispatch: decision is not ACCEPT")

// ErrNoDownloaderForProtocol reports no enabled downloader matching the
// candidate's own protocol -- a usenet release with only a torrent client
// configured, for instance.
var ErrNoDownloaderForProtocol = errors.New("music: dispatch: no enabled downloader for this candidate's protocol")

// DownloaderConn is one configured, enabled download client ready to
// dispatch to -- building the client (credentials, protocol) is the
// caller's own job, the same split IndexerConn already keeps for indexers.
type DownloaderConn struct {
	ID       int64
	Protocol string // "usenet" | "torrent" | "ddl" (ADR-46) -- only usenet has a sender today
	Client   download.Client
	// Category is the client category music is sent under, "music" unless the client says otherwise.
	Category string
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
}

// Send dispatches one decision, previously recorded against itemID by
// Search.SearchItem, to whichever configured downloader matches its own
// protocol.
func (d Dispatch) Send(ctx context.Context, itemID, decisionID int64, at time.Time) (library.Acquisition, error) {
	if _, err := d.Desired.Get(ctx, itemID); err != nil {
		return library.Acquisition{}, fmt.Errorf("music: dispatch: %w", err)
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
		return library.Acquisition{}, fmt.Errorf("music: dispatch: %w", err)
	} else if inFlight {
		return library.Acquisition{}, library.ErrAcquisitionAlreadyInFlight
	}

	downloaders, err := d.Downloaders(ctx)
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("music: dispatch: downloaders: %w", err)
	}
	downloader, found := firstMatchingProtocol(downloaders, detail.ReleaseCandidate.Protocol)
	if !found {
		return library.Acquisition{}, ErrNoDownloaderForProtocol
	}

	category := downloader.Category
	file, err := d.Fetch(ctx, detail.ReleaseCandidate.IndexerID, detail.ReleaseCandidate.DownloadURL)
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("music: dispatch: fetch release: %w", err)
	}
	clientRef, err := downloader.Client.Add(ctx, download.AddRequest{
		Name: detail.ReleaseCandidate.Title, File: file, Category: category,
	})
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("music: dispatch: %w", err)
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
		CreatedAt: at.Unix(), UpdatedAt: at.Unix(),
	})
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("music: dispatch: %w", err)
	}
	if d.History != nil {
		d.History(ctx, download.HistoryGrabbed, created, "")
	}
	return created, nil
}

func (d Dispatch) findDecision(ctx context.Context, itemID, decisionID int64) (library.DecisionDetail, error) {
	details, err := d.Candidates.ListDecisionsForItem(ctx, itemID, dispatchDecisionLookupLimit)
	if err != nil {
		return library.DecisionDetail{}, fmt.Errorf("music: dispatch: list decisions: %w", err)
	}
	for _, detail := range details {
		if detail.Decision.ID == decisionID {
			return detail, nil
		}
	}
	return library.DecisionDetail{}, ErrDecisionNotFound
}

// dispatchDecisionLookupLimit bounds findDecision's own lookup -- generous
// for a manual, low-frequency action; the same ceiling
// adapters.CandidateSearch's own history listing already uses.
const dispatchDecisionLookupLimit = 100

func firstMatchingProtocol(downloaders []DownloaderConn, protocol string) (DownloaderConn, bool) {
	for _, downloader := range downloaders {
		if downloader.Protocol == protocol {
			return downloader, true
		}
	}
	return DownloaderConn{}, false
}
