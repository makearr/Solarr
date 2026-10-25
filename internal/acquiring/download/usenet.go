package download

import (
	"bytes"
	"context"
	"errors"
)

// UsenetClient is what differs from one usenet client to the next (Sonarr's UsenetClientBase subclasses); NewUsenet
// supplies the rest.
type UsenetClient interface {
	Prober
	// AddNZB sends one NZB Solarr fetched itself, named after the release, answering the client's reference for it.
	AddNZB(ctx context.Context, request AddRequest) (clientRef string, err error)
	Downloads(ctx context.Context, category string) ([]Download, error)
	QueueStatus(ctx context.Context) (QueueStatus, error)
	Remove(ctx context.Context, clientRef string, deleteFiles bool) error
	Pause(ctx context.Context, clientRefs []string) error
	Resume(ctx context.Context, clientRefs []string) error
}

// ErrNotNZB is a release whose file is not an NZB: an indexer's error page, most often.
var ErrNotNZB = errors.New("download: the release is not an NZB")

// NewUsenet puts a usenet client behind Driver.
func NewUsenet(client UsenetClient) Driver { return usenetBase{client: client} }

type usenetBase struct{ client UsenetClient }

var _ Driver = usenetBase{}

// Add refuses what is plainly not an NZB before any client sees it, as Sonarr's ValidateNzb does.
func (b usenetBase) Add(ctx context.Context, request AddRequest) (string, error) {
	if !bytes.Contains(bytes.ToLower(request.File), []byte("<nzb")) {
		return "", ErrNotNZB
	}
	return b.client.AddNZB(ctx, request)
}

func (b usenetBase) Downloads(ctx context.Context, category string) ([]Download, error) {
	return b.client.Downloads(ctx, category)
}

func (b usenetBase) QueueStatus(ctx context.Context) (QueueStatus, error) {
	return b.client.QueueStatus(ctx)
}

func (b usenetBase) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return b.client.Remove(ctx, clientRef, deleteFiles)
}

func (b usenetBase) Pause(ctx context.Context, clientRefs []string) error {
	if len(clientRefs) == 0 {
		return nil
	}
	return b.client.Pause(ctx, clientRefs)
}

func (b usenetBase) Resume(ctx context.Context, clientRefs []string) error {
	if len(clientRefs) == 0 {
		return nil
	}
	return b.client.Resume(ctx, clientRefs)
}

func (b usenetBase) Probe(ctx context.Context) (Probe, error) { return b.client.Probe(ctx) }
