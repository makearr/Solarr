// Package dispatch sends a decided release to a download client and holds it when the client is full: every media
// type's, extracted from music for stage 8.
package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/statistics"
	"github.com/makearr/Solarr/internal/platform/httpx"
	"github.com/makearr/Solarr/internal/platform/keyedlock"
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

// ErrReleaseUnavailable is a release its indexer no longer serves (404, 410, an item-gone answer): it will not come back,
// so the next release is tried and this one is left out for a day.
var ErrReleaseUnavailable = errors.New("dispatch: the indexer no longer serves this release")

// ErrReleaseFetchFailed is a release that could not be fetched this time (a timeout, a 5xx, an error answer that is the
// indexer's own trouble): the next release is tried, and this one stays a candidate.
var ErrReleaseFetchFailed = errors.New("dispatch: the release could not be fetched")

// ErrDownloadsPaused is a send refused because the instance's mode keeps downloads paused.
var ErrDownloadsPaused = errors.New("dispatch: downloads are paused by the mode")

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
	// Priority is the client's, 1 to 50: of the clients that take a release the lowest number is sent to first.
	Priority int
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

// Dispatch is ADR-57's acquire.send: it submits an ACCEPTed decision's release to a matching downloader and records the acquisition; no resource gate yet, since every dispatch is a user action (ADR-12 "never paused").
type Dispatch struct {
	// Candidates are read only to send what a person picked by its decision.
	Candidates   statistics.Candidates
	Acquisitions library.Acquisitions
	Desired      library.Desired
	Downloaders  DownloaderSource
	// BoundClient answers the download client an indexer is bound to, 0 for none: its releases go there.
	BoundClient func(ctx context.Context, indexerID int64) int64
	Fetch       ReleaseFetcher
	// History is optional: told of every grab, for History and the album's page.
	History func(ctx context.Context, kind download.HistoryKind, acquisition library.Acquisition, detail string)
	// MediaType is the type this dispatch sends for, which SendHeld takes held releases of; empty takes every type's.
	MediaType string
	// Ready is optional: where an automatic send is held while its client's queue is full.
	Ready *library.ReadyQueue
	// ReadyLimit is optional: how many releases Ready may hold (DefaultReadyLimit when nil).
	ReadyLimit func() int64
	// Blocklist is optional: a held release blocklisted meanwhile is dropped, not sent.
	Blocklist *library.BlocklistStore
	// Admits is optional: whether a library's maximum rating lets in a work admitting age (-1 unrated); an automatic
	// grab of a work it does not admit is not sent.
	Admits func(categoryID, age int64) bool
	// Paused is optional: true while the mode sends nothing to a download client.
	Paused func() bool
	// PartsOf is optional: the media refs of the item a release holds, for a media type whose items are sets (a TV season),
	// so several downloads of one item may run together while they do not overlap. Without it, or empty, a release is the whole item.
	PartsOf func(ctx context.Context, itemID int64, release library.ReleaseCandidate) []string
	// Locks is optional: one send per item at a time, shared by every Dispatch of the process, so two senders cannot
	// both pass the in-flight check and put the same release in the client twice.
	Locks *keyedlock.Locks
}

// Send dispatches one decision, previously recorded against itemID by
// Search.SearchItem, to whichever configured downloader matches its own
// protocol.
func (d Dispatch) Send(ctx context.Context, itemID, decisionID int64, at time.Time) (library.Acquisition, error) {
	detail, err := d.findDecision(ctx, itemID, decisionID)
	if err != nil {
		return library.Acquisition{}, err
	}
	if detail.Decision.Outcome != "ACCEPT" {
		return library.Acquisition{}, ErrDecisionNotAccepted
	}
	return d.SendRelease(ctx, itemID, detail.ReleaseCandidate, at)
}

// SendRelease sends one accepted release for the item, needing nothing but the release itself: a held one carries
// it, so dispatch never reads where it was decided.
func (d Dispatch) SendRelease(ctx context.Context, itemID int64, release library.ReleaseCandidate, at time.Time) (library.Acquisition, error) {
	if d.Paused != nil && d.Paused() {
		return library.Acquisition{}, ErrDownloadsPaused
	}
	detail := library.DecisionDetail{ReleaseCandidate: release}
	if _, err := d.Desired.Get(ctx, itemID); err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
	}

	if d.Locks != nil {
		defer d.Locks.Lock(fmt.Sprintf("send:%d", itemID))()
	}
	// Before the client is involved: refusing after the send leaves a duplicate in its queue.
	parts := d.partsOf(ctx, itemID, release)
	if conflict, err := d.Acquisitions.Conflicts(ctx, itemID, parts); err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
	} else if conflict {
		return library.Acquisition{}, library.ErrAcquisitionAlreadyInFlight
	}

	downloaders, err := d.Downloaders(ctx)
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: downloaders: %w", err)
	}
	downloader, found, err := d.choose(ctx, downloaders, detail.ReleaseCandidate.IndexerID, detail.ReleaseCandidate.Protocol)
	if err != nil {
		return library.Acquisition{}, fmt.Errorf("dispatch: %w", err)
	}
	if !found {
		return library.Acquisition{}, ErrNoDownloaderForProtocol
	}

	category := downloader.Category
	file, err := d.Fetch(ctx, detail.ReleaseCandidate.IndexerID, detail.ReleaseCandidate.DownloadURL)
	if err != nil && detail.ReleaseCandidate.Protocol == "torrent" && detail.ReleaseCandidate.MagnetURL != "" {
		// A dead .torrent link does not make the release unavailable when the indexer also listed its magnet link.
		if magnet, magnetErr := d.Fetch(ctx, detail.ReleaseCandidate.IndexerID, detail.ReleaseCandidate.MagnetURL); magnetErr == nil {
			file, err = magnet, nil
		}
	}
	if err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) && (status.Status == http.StatusNotFound || status.Status == http.StatusGone) {
			return library.Acquisition{}, fmt.Errorf("%w: %w", ErrReleaseUnavailable, err)
		}
		return library.Acquisition{}, fmt.Errorf("%w: %w", ErrReleaseFetchFailed, err)
	}
	if err := download.CheckRelease(detail.ReleaseCandidate.Protocol, file); err != nil {
		// What came back is not a release, so no client is bothered with it.
		var notRelease *download.NotAReleaseError
		if errors.As(err, &notRelease) && notRelease.Gone() {
			return library.Acquisition{}, fmt.Errorf("%w: %w", ErrReleaseUnavailable, err)
		}
		return library.Acquisition{}, fmt.Errorf("%w: %w", ErrReleaseFetchFailed, err)
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
	clientRef, err := d.addOrAdopt(ctx, downloader.Client, detail.ReleaseCandidate.Protocol, download.AddRequest{
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
	// Not cancelled with the caller: between the client accepting the release and this row, a stop would orphan it.
	created, err := d.Acquisitions.Create(context.WithoutCancel(ctx), library.NewAcquisition{
		DesiredItemID: &desiredItemID, CandidateID: &candidateID, IndexerID: &indexerID,
		CandidateGUID: detail.ReleaseCandidate.GUID, CandidateTitle: detail.ReleaseCandidate.Title,
		DownloaderID: downloader.ID, DownloadClientRef: clientRef, ClientCategory: category,
		// section 10.9's own "added time + name + size" client fingerprint --
		// a reference reused for a different download no longer matches this.
		ClientFingerprint: fmt.Sprintf("%d:%s:%d", at.Unix(), detail.ReleaseCandidate.Title, size),
		Protocol:          detail.ReleaseCandidate.Protocol,
		// QUEUED, not DOWNLOADING: acquire.send only confirms the client accepted the job, and telling them apart needs client polling (section 10.9), not built yet.
		State:     "QUEUED",
		CreatedAt: at.Unix(), UpdatedAt: at.Unix(), ContentHash: contentHash, SizeBytes: size, Parts: parts,
	})
	if err != nil {
		// In the client with no row it would never be followed, imported or removed.
		removeErr := downloader.Client.Remove(context.WithoutCancel(ctx), clientRef, true)
		return library.Acquisition{}, errors.Join(fmt.Errorf("dispatch: %w", err), removeErr)
	}
	if d.History != nil {
		d.History(ctx, download.HistoryGrabbed, created, "")
	}
	return created, nil
}

// addOrAdopt sends the release, except that a torrent the client already holds in our category is taken as sent: the
// refusal of a duplicate must not make the next, worse release go out beside it.
func (d Dispatch) addOrAdopt(ctx context.Context, client download.Client, protocol string, request download.AddRequest) (string, error) {
	if protocol != "torrent" {
		return client.Add(ctx, request)
	}
	if ref, err := adoptTorrent(ctx, client, request.File, request.Category); err != nil || ref != "" {
		return ref, err
	}
	ref, err := client.Add(ctx, request)
	if !errors.Is(err, download.ErrRefused) {
		return ref, err
	}
	if adopted, adoptErr := adoptTorrent(ctx, client, request.File, request.Category); adoptErr != nil || adopted != "" {
		return adopted, adoptErr
	}
	return ref, err
}

// partsOf is what a release holds of the item; nil is the whole item.
func (d Dispatch) partsOf(ctx context.Context, itemID int64, release library.ReleaseCandidate) []string {
	if d.PartsOf == nil {
		return nil
	}
	return d.PartsOf(ctx, itemID, release)
}

func (d Dispatch) findDecision(ctx context.Context, itemID, decisionID int64) (library.DecisionDetail, error) {
	detail, err := d.Candidates.DecisionForItem(ctx, itemID, decisionID)
	if errors.Is(err, sql.ErrNoRows) {
		return library.DecisionDetail{}, ErrDecisionNotFound
	}
	if err != nil {
		return library.DecisionDetail{}, fmt.Errorf("dispatch: %w", err)
	}
	return detail, nil
}

// DecisionLookupLimit bounds a listing of an item's recent decisions; the same ceiling adapters.CandidateSearch's own
// history listing uses.
const DecisionLookupLimit = 100
