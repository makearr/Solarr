package download

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// DirectRelease is what a direct download is sent as: the peer to fetch from and the files it shares, as a Soulseek
// search answers them. A direct-download indexer's release file is this, as JSON.
type DirectRelease struct {
	// Source is the peer: a Soulseek user name.
	Source string       `json:"source"`
	Files  []DirectFile `json:"files"`
}

// DirectFile is one file of a direct release, by the peer's own path.
type DirectFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ErrNotDirectRelease is a release whose file is not a direct release.
var ErrNotDirectRelease = errors.New("download: the release is not a direct download")

// ParseDirectRelease reads a direct release; one without a source or files is refused.
func ParseDirectRelease(file []byte) (DirectRelease, error) {
	var release DirectRelease
	if err := json.Unmarshal(file, &release); err != nil || strings.TrimSpace(release.Source) == "" || len(release.Files) == 0 {
		return DirectRelease{}, ErrNotDirectRelease
	}
	return release, nil
}

// DirectClient is what differs from one direct-download client to the next; NewDirect supplies the rest.
type DirectClient interface {
	Prober
	// AddDirect sends one parsed release, answering the client's reference for it.
	AddDirect(ctx context.Context, release DirectRelease, request AddRequest) (clientRef string, err error)
	Downloads(ctx context.Context, category string) ([]Download, error)
	Remove(ctx context.Context, clientRef string, deleteFiles bool) error
	Pause(ctx context.Context, clientRefs []string) error
	Resume(ctx context.Context, clientRefs []string) error
}

// NewDirect puts a direct-download client behind Driver.
func NewDirect(client DirectClient) Driver { return directBase{client: client} }

type directBase struct{ client DirectClient }

var _ Driver = directBase{}

// Add reads the release first, so no client sees a file that names no peer or no files.
func (b directBase) Add(ctx context.Context, request AddRequest) (string, error) {
	release, err := ParseDirectRelease(request.File)
	if err != nil {
		return "", err
	}
	return b.client.AddDirect(ctx, release, request)
}

func (b directBase) Downloads(ctx context.Context, category string) ([]Download, error) {
	return b.client.Downloads(ctx, category)
}

// QueueStatus answers the totals over every download still running.
func (b directBase) QueueStatus(ctx context.Context) (QueueStatus, error) {
	downloads, err := b.client.Downloads(ctx, "")
	if err != nil {
		return QueueStatus{}, err
	}
	return queueTotals(downloads), nil
}

func (b directBase) Remove(ctx context.Context, clientRef string, deleteFiles bool) error {
	return b.client.Remove(ctx, clientRef, deleteFiles)
}

func (b directBase) Pause(ctx context.Context, clientRefs []string) error {
	if len(clientRefs) == 0 {
		return nil
	}
	return b.client.Pause(ctx, clientRefs)
}

func (b directBase) Resume(ctx context.Context, clientRefs []string) error {
	if len(clientRefs) == 0 {
		return nil
	}
	return b.client.Resume(ctx, clientRefs)
}

func (b directBase) Probe(ctx context.Context) (Probe, error) { return b.client.Probe(ctx) }
