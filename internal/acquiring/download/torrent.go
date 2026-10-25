package download

import (
	"context"
	"strings"
)

// TorrentClient is what differs from one torrent client to the next (Sonarr's TorrentClientBase subclasses);
// NewTorrent supplies the rest: the info hash, the magnet-or-file choice, the queue totals.
type TorrentClient interface {
	Prober
	// AddTorrentFile and AddMagnet send one torrent whose info hash is known, answering the client's reference for it.
	AddTorrentFile(ctx context.Context, hash string, request AddRequest) (clientRef string, err error)
	AddMagnet(ctx context.Context, hash, magnet string, request AddRequest) (clientRef string, err error)
	Downloads(ctx context.Context, category string) ([]Download, error)
	Remove(ctx context.Context, clientRef string, deleteFiles bool) error
	Pause(ctx context.Context, clientRefs []string) error
	Resume(ctx context.Context, clientRefs []string) error
}

// TransferSpeed is a client that reports its overall download speed; without it the downloads' own speeds are summed.
type TransferSpeed interface {
	TransferSpeed(ctx context.Context) (bytesPerSec int64, err error)
}

// NewTorrent puts a torrent client behind Driver; one that can name finished files keeps FinishedFiles.
func NewTorrent(client TorrentClient) Driver {
	base := torrentBase{client: client}
	if finisher, ok := client.(FinishedFiles); ok {
		return torrentWithFinishedFiles{torrentBase: base, finisher: finisher}
	}
	return base
}

type torrentBase struct{ client TorrentClient }

var _ Driver = torrentBase{}

// Add reads the info hash first, so every torrent is tracked by it whatever the client answers.
func (b torrentBase) Add(ctx context.Context, request AddRequest) (string, error) {
	hash, err := InfoHash(request.File)
	if err != nil {
		return "", err
	}
	if magnet := strings.TrimSpace(string(request.File)); strings.HasPrefix(magnet, "magnet:") {
		return b.client.AddMagnet(ctx, hash, magnet, request)
	}
	return b.client.AddTorrentFile(ctx, hash, request)
}

func (b torrentBase) Downloads(ctx context.Context, category string) ([]Download, error) {
	return b.client.Downloads(ctx, category)
}

// QueueStatus answers the totals over every torrent still downloading.
func (b torrentBase) QueueStatus(ctx context.Context) (QueueStatus, error) {
	downloads, err := b.client.Downloads(ctx, "")
	if err != nil {
		return QueueStatus{}, err
	}
	status := queueTotals(downloads)
	if reporter, ok := b.client.(TransferSpeed); ok {
		if status.SpeedBytesPerSec, err = reporter.TransferSpeed(ctx); err != nil {
			return QueueStatus{}, err
		}
	}
	return status, nil
}

// queueTotals sums what is still to come over the downloads not yet finished, and their speeds.
func queueTotals(downloads []Download) QueueStatus {
	var status QueueStatus
	for _, one := range downloads {
		if one.Part {
			continue
		}
		status.SpeedBytesPerSec += one.SpeedBytesPerSec
		if one.SizeLeftBytes > 0 && one.State != StateFailed && one.State != StateCompleted {
			status.Items++
			status.SizeLeftBytes += one.SizeLeftBytes
		}
	}
	return status
}

func (b torrentBase) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return b.client.Remove(ctx, clientRef, deleteFiles)
}

// Pause and Resume do nothing for no downloads, so a client is never asked to act on all of them.
func (b torrentBase) Pause(ctx context.Context, clientRefs []string) error {
	if len(clientRefs) == 0 {
		return nil
	}
	return b.client.Pause(ctx, clientRefs)
}

func (b torrentBase) Resume(ctx context.Context, clientRefs []string) error {
	if len(clientRefs) == 0 {
		return nil
	}
	return b.client.Resume(ctx, clientRefs)
}

func (b torrentBase) Probe(ctx context.Context) (Probe, error) { return b.client.Probe(ctx) }

type torrentWithFinishedFiles struct {
	torrentBase
	finisher FinishedFiles
}

func (b torrentWithFinishedFiles) FinishedFiles(ctx context.Context, clientRef string) (string, []string, error) {
	return b.finisher.FinishedFiles(ctx, clientRef)
}

// TorrentPath is where a finished torrent's content is when a client reports the folder it was saved in: a
// multi-file torrent's own folder already ends in its name, a single file lies directly in it.
func TorrentPath(folder, name string) string {
	trimmed := strings.TrimRight(folder, "/\\")
	if trimmed == "" || name == "" {
		return trimmed
	}
	if last := trimmed[strings.LastIndexAny(trimmed, "/\\")+1:]; last == name {
		return trimmed
	}
	separator := "/"
	if strings.Contains(trimmed, "\\") && !strings.Contains(trimmed, "/") {
		separator = "\\"
	}
	return trimmed + separator + name
}
