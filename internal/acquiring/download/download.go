// Package download is the seam every download client sits behind (ADR-46): SABnzbd today,
// qBittorrent (D2) and a DDL client later. Solarr implements no download client of its own; it
// sends, watches and cleans up, by client reference and never by path (section 10.9).
package download

import "context"

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
)

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

// Client is one configured download client.
type Client interface {
	// Add sends a release and answers the client's reference for it.
	Add(ctx context.Context, request AddRequest) (clientRef string, err error)
	// Downloads answers what the client holds in category, queued and recently finished.
	Downloads(ctx context.Context, category string) ([]Download, error)
	QueueStatus(ctx context.Context) (QueueStatus, error)
	// Remove takes a download out of the client; deleteFiles also deletes what it downloaded.
	Remove(ctx context.Context, clientRef string, deleteFiles bool) error
}
