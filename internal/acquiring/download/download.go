// Package download is the seam every download client sits behind (ADR-46), and the base every client kind extends
// (definition.go, torrent.go, usenet.go). Solarr implements no download client of its own; it
// sends, watches and cleans up, by client reference and never by path (section 10.9).
package download

import (
	"context"
	"errors"
)

// State is where a download stands, in Solarr's words rather than any one client's.
type State string

// The states a client's own vocabulary maps onto.
const (
	StateQueued      State = "queued"
	StateDownloading State = "downloading"
	StatePaused      State = "paused"
	// StateProcessing is finished downloading and not yet ready: verifying, repairing, unpacking.
	StateProcessing State = "processing"
	StateCompleted  State = "completed"
	StateFailed     State = "failed"
	// StateRemoved is a download the client dropped for a reason that is not the release's fault: a person deleted
	// it, or the client already had that release. It is neither blocklisted nor searched for again.
	StateRemoved State = "removed"
)

// HistoryKind is what happened to a download, as History and an album's page show it (section 5.7).
type HistoryKind string

// The history kinds M1's acceptance test names: "grabbed" and "imported", a failure, and a removal
// from the queue by a person.
const (
	HistoryGrabbed  HistoryKind = "download.grabbed"
	HistoryImported HistoryKind = "download.imported"
	HistoryFailed   HistoryKind = "download.failed"
	HistoryRemoved  HistoryKind = "download.removed"
)

// HistoryKinds are every download history kind, newest-first listings read all of them.
var HistoryKinds = []HistoryKind{HistoryGrabbed, HistoryImported, HistoryFailed, HistoryRemoved}

// HistoryData is a history entry's payload: what was known when it happened, kept as it was.
type HistoryData struct {
	AcquisitionID int64  `json:"acquisition_id"`
	CategoryID    int64  `json:"category_id,omitempty"`
	ReleaseTitle  string `json:"release_title"`
	Album         string `json:"album,omitempty"`
	Artist        string `json:"artist,omitempty"`
	Indexer       string `json:"indexer,omitempty"`
	Client        string `json:"client,omitempty"`
	Detail        string `json:"detail,omitempty"`
	// Upgrade is true when the item already held everything it requires, so the download replaces files.
	Upgrade bool `json:"upgrade,omitempty"`
}

// ErrRefused is a client declining a release it was sent: SABnzbd answers status false, often
// without a reason, for one it has downloaded before (found live 2026-09-29).
var ErrRefused = errors.New("the download client refused the release")

// Download is one download as the client reports it.
type Download struct {
	ClientRef     string
	Name          string
	Category      string
	State         State
	SizeBytes     int64
	SizeLeftBytes int64
	// Path is where a completed download's files are; empty until then.
	Path string
	// Error is the client's reason for a failure.
	Error string
	// LocalError means the client could not write or read its own files (a full disk, an unmounted share); it says nothing about the release.
	LocalError bool
	// Ratio and SeedingSeconds are what a torrent has shared and for how long; zero for a client that does not seed.
	Ratio          float64
	SeedingSeconds int64
	// SpeedBytesPerSec is how fast it is downloading now; zero where the client does not say.
	SpeedBytesPerSec int64
	// Part is an entry for one file of a download the client also reports whole, so a release of that file alone
	// can be followed; queue totals leave it out.
	Part bool
}

// AddRequest is one release sent to a client: the file Solarr fetched from the indexer, named.
type AddRequest struct {
	Name     string
	File     []byte
	Category string
}

// QueueStatus is the client's own queue, for ADR-12's pressure signals and the Downloads page.
type QueueStatus struct {
	Paused           bool
	SpeedBytesPerSec int64
	Items            int
	SizeLeftBytes    int64
}

// FinishedFiles is a client that can say which files of an unfinished download are complete, so a download that
// stalled can still bring in what did arrive. Folder is the download's own folder as the client names it, files are
// relative to it; a download with no folder of its own (one file, or files loose in the client's folder) answers none.
type FinishedFiles interface {
	FinishedFiles(ctx context.Context, clientRef string) (folder string, files []string, err error)
}

// OwnFiles is a client whose download folder is not the download's alone (Soulseek saves every folder of one name to
// the same place): it answers the names of the files this download brought, so an import takes those and no others.
type OwnFiles interface {
	OwnFiles(ctx context.Context, clientRef string) ([]string, error)
}

// ByReference is a client that can answer for named downloads alone. Asking it is exact where listing a category is
// not: a category lists only a window of a shared client's history, and a finished download can drop out of it.
type ByReference interface {
	// DownloadsByRef answers the downloads with these references; one the client no longer has is simply absent.
	DownloadsByRef(ctx context.Context, clientRefs []string) ([]Download, error)
}

// ErrByRefUnsupported is a driver whose underlying client cannot answer by reference; the caller lists by category instead.
var ErrByRefUnsupported = errors.New("download: the client cannot list downloads by reference")

// Client is one configured download client.
type Client interface {
	// Add sends a release and answers the client's reference for it.
	Add(ctx context.Context, request AddRequest) (clientRef string, err error)
	// Downloads answers what the client holds in category, queued and recently finished.
	Downloads(ctx context.Context, category string) ([]Download, error)
	QueueStatus(ctx context.Context) (QueueStatus, error)
	// Remove takes a download out of the client; deleteFiles also deletes what it downloaded.
	Remove(ctx context.Context, clientRef string, deleteFiles bool) error
	// Pause and Resume act on the named downloads only, never the whole client: other apps share it.
	Pause(ctx context.Context, clientRefs []string) error
	Resume(ctx context.Context, clientRefs []string) error
}
