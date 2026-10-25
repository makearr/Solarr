// Package slskd is the REST client of slskd, the Soulseek daemon, behind download.DirectClient (ADR-46): Solarr asks
// a peer for a folder's files and follows their transfers as one download.
package slskd

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 16 << 20

// Client is a thin REST client over one connection; apiKey is one of slskd's web.authentication.api_keys.
type Client struct {
	http   *httpx.Client
	apiURL string
	apiKey string
}

// NewClient builds one over an already-configured httpx.Client.
func NewClient(client *httpx.Client, baseURL, apiKey string) Client {
	return Client{http: client, apiURL: strings.TrimRight(baseURL, "/") + "/api/v0", apiKey: apiKey}
}

func (c Client) call(ctx context.Context, method, path string, payload any, into any) error {
	var body []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = encoded
	}
	request, err := http.NewRequestWithContext(ctx, method, c.apiURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("X-API-Key", c.apiKey)
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("slskd: read %s: %w", path, err)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("slskd: decode %s: %w", path, err)
	}
	return nil
}

// Version answers slskd's version; every call needs the key, so it proves it too.
func (c Client) Version(ctx context.Context) (string, error) {
	var application struct {
		Version struct {
			Full    string `json:"full"`
			Current string `json:"current"`
		} `json:"version"`
	}
	if err := c.call(ctx, http.MethodGet, "/application", nil, &application); err != nil {
		return "", err
	}
	if application.Version.Current != "" {
		return application.Version.Current, nil
	}
	return application.Version.Full, nil
}

// DownloadsDirectory answers where slskd saves finished downloads.
func (c Client) DownloadsDirectory(ctx context.Context) (string, error) {
	var options struct {
		Directories struct {
			Downloads string `json:"downloads"`
		} `json:"directories"`
	}
	err := c.call(ctx, http.MethodGet, "/options", nil, &options)
	return options.Directories.Downloads, err
}

// FileRequest is one file asked of a peer.
type FileRequest struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
}

// Enqueue asks username for files.
func (c Client) Enqueue(ctx context.Context, username string, files []FileRequest) error {
	return c.call(ctx, http.MethodPost, "/transfers/downloads/"+url.PathEscape(username), files, nil)
}

// Transfer is one file's transfer.
type Transfer struct {
	ID               string  `json:"id"`
	Username         string  `json:"username"`
	Filename         string  `json:"filename"`
	Size             int64   `json:"size"`
	State            string  `json:"state"`
	BytesTransferred int64   `json:"bytesTransferred"`
	BytesRemaining   int64   `json:"bytesRemaining"`
	AverageSpeed     float64 `json:"averageSpeed"`
	Exception        string  `json:"exception"`
}

// Directory is a peer's folder with the transfers asked of it.
type Directory struct {
	Directory string     `json:"directory"`
	Files     []Transfer `json:"files"`
}

// UserTransfers are every transfer asked of one peer, by folder.
type UserTransfers struct {
	Username    string      `json:"username"`
	Directories []Directory `json:"directories"`
}

// Downloads answers every download transfer slskd holds.
func (c Client) Downloads(ctx context.Context) ([]UserTransfers, error) {
	var users []UserTransfers
	err := c.call(ctx, http.MethodGet, "/transfers/downloads", nil, &users)
	return users, err
}

// RemoveTransfer cancels one transfer and forgets it.
func (c Client) RemoveTransfer(ctx context.Context, username, id string) error {
	return c.call(ctx, http.MethodDelete, "/transfers/downloads/"+url.PathEscape(username)+"/"+url.PathEscape(id)+"?remove=true", nil, nil)
}

// DeleteDirectory deletes one folder below the downloads directory, named relative to it; one already gone is not an
// error.
func (c Client) DeleteDirectory(ctx context.Context, relative string) error {
	encoded := base64.URLEncoding.EncodeToString([]byte(relative))
	err := c.call(ctx, http.MethodDelete, "/files/downloads/directories/"+encoded, nil, nil)
	var status *httpx.StatusError
	if errors.As(err, &status) && status.Status == http.StatusNotFound {
		return nil
	}
	return err
}
