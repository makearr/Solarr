// Package transmission is the RPC client of Transmission, and of Vuze through its Transmission-compatible web remote,
// behind download.TorrentClient (ADR-46). As with sabnzbd, the caller supplies an already-configured httpx.Client.
package transmission

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/acquiring/download"
	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 8 << 20

// DefaultURLBase is where Transmission and Vuze serve their RPC unless told otherwise.
const DefaultURLBase = "/transmission/"

// sessionHeader is Transmission's CSRF token: the first call is refused with 409 and the token to send.
const sessionHeader = "X-Transmission-Session-Id"

// Client is a thin RPC client over one connection; credentials are "user:password", or empty.
type Client struct {
	http     *httpx.Client
	rpcURL   string
	username string
	password string
	session  *string
}

// NewClient builds one; urlBase is the RPC's path, empty for DefaultURLBase.
func NewClient(client *httpx.Client, baseURL, urlBase, credentials string) Client {
	if strings.TrimSpace(urlBase) == "" {
		urlBase = DefaultURLBase
	}
	user, password, _ := strings.Cut(credentials, ":")
	rpcURL := strings.TrimRight(baseURL, "/") + "/" + strings.Trim(urlBase, "/") + "/rpc"
	return Client{http: client, rpcURL: rpcURL, username: user, password: password, session: new(string)}
}

type rpcRequest struct {
	Method    string `json:"method"`
	Arguments any    `json:"arguments,omitempty"`
}

type rpcResponse struct {
	Result    string          `json:"result"`
	Arguments json.RawMessage `json:"arguments"`
}

// call performs one RPC method, fetching the session token once when Transmission asks for it.
func (c Client) call(ctx context.Context, method string, arguments any, into any) error {
	payload, err := json.Marshal(rpcRequest{Method: method, Arguments: arguments})
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		if *c.session != "" {
			request.Header.Set(sessionHeader, *c.session)
		}
		if c.username != "" || c.password != "" {
			request.SetBasicAuth(c.username, c.password)
		}
		response, err := c.http.Do(ctx, request)
		var refused *httpx.StatusError
		if errors.As(err, &refused) && refused.Status == http.StatusConflict && attempt == 0 {
			*c.session = refused.Header.Get(sessionHeader)
			continue
		}
		if err != nil {
			return err
		}
		body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
		_ = response.Body.Close()
		if err != nil {
			return fmt.Errorf("transmission: read %s: %w", method, err)
		}
		var parsed rpcResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return fmt.Errorf("transmission: decode %s: %w", method, err)
		}
		if parsed.Result != "success" {
			return fmt.Errorf("transmission: %s: %s", method, parsed.Result)
		}
		if into == nil || len(parsed.Arguments) == 0 {
			return nil
		}
		if err := json.Unmarshal(parsed.Arguments, into); err != nil {
			return fmt.Errorf("transmission: decode %s: %w", method, err)
		}
		return nil
	}
}

// Session is the part of session-get a connection test and an add need.
type Session struct {
	Version     string `json:"version"`
	RPCVersion  int    `json:"rpc-version"`
	DownloadDir string `json:"download-dir"`
}

// Session answers the client's own settings; it needs the login, so it proves it too.
func (c Client) Session(ctx context.Context) (Session, error) {
	var session Session
	err := c.call(ctx, "session-get", nil, &session)
	return session, err
}

// AddRequest is one torrent file, or a magnet link in Magnet, saved below DownloadDir ("" is the client's default).
type AddRequest struct {
	File        []byte
	Magnet      string
	DownloadDir string
}

type addArguments struct {
	MetaInfo    string `json:"metainfo,omitempty"`
	Filename    string `json:"filename,omitempty"`
	DownloadDir string `json:"download-dir,omitempty"`
	Paused      bool   `json:"paused"`
}

// Add sends one torrent; one the client already has counts as sent, since it is tracked by its info hash anyway.
func (c Client) Add(ctx context.Context, request AddRequest) error {
	arguments := addArguments{Filename: request.Magnet, DownloadDir: request.DownloadDir}
	if request.Magnet == "" {
		arguments.MetaInfo = base64.StdEncoding.EncodeToString(request.File)
	}
	err := c.call(ctx, "torrent-add", arguments, nil)
	if err != nil && strings.Contains(err.Error(), "duplicate torrent") {
		return nil
	}
	if err != nil {
		var status *httpx.StatusError
		if errors.As(err, &status) {
			return err
		}
		return fmt.Errorf("%w: %w", download.ErrRefused, err)
	}
	return nil
}

// Torrent is the part of torrent-get one download needs.
type Torrent struct {
	HashString     string  `json:"hashString"`
	Name           string  `json:"name"`
	DownloadDir    string  `json:"downloadDir"`
	Status         int     `json:"status"`
	TotalSize      int64   `json:"totalSize"`
	LeftUntilDone  int64   `json:"leftUntilDone"`
	IsFinished     bool    `json:"isFinished"`
	Error          int     `json:"error"`
	ErrorString    string  `json:"errorString"`
	UploadRatio    float64 `json:"uploadRatio"`
	SecondsSeeding int64   `json:"secondsSeeding"`
	RateDownload   int64   `json:"rateDownload"`
	// FileCount is reported by Vuze, which saves a multi-file torrent in a folder of its own and names that folder.
	FileCount int `json:"fileCount"`
}

var torrentFields = []string{
	"hashString", "name", "downloadDir", "status", "totalSize", "leftUntilDone", "isFinished", "error", "errorString",
	"uploadRatio", "secondsSeeding", "rateDownload", "fileCount",
}

// Torrents answers every torrent, or those named by hash.
func (c Client) Torrents(ctx context.Context, hashes []string) ([]Torrent, error) {
	arguments := struct {
		Fields []string `json:"fields"`
		IDs    []string `json:"ids,omitempty"`
	}{Fields: torrentFields, IDs: hashes}
	var parsed struct {
		Torrents []Torrent `json:"torrents"`
	}
	if err := c.call(ctx, "torrent-get", arguments, &parsed); err != nil {
		return nil, err
	}
	return parsed.Torrents, nil
}

// Remove deletes torrents by hash; deleteData also deletes what they downloaded.
func (c Client) Remove(ctx context.Context, hashes []string, deleteData bool) error {
	return c.call(ctx, "torrent-remove", struct {
		IDs        []string `json:"ids"`
		DeleteData bool     `json:"delete-local-data"`
	}{hashes, deleteData}, nil)
}

// SetPaused stops or starts torrents by hash.
func (c Client) SetPaused(ctx context.Context, hashes []string, paused bool) error {
	method := "torrent-start"
	if paused {
		method = "torrent-stop"
	}
	return c.call(ctx, method, struct {
		IDs []string `json:"ids"`
	}{hashes}, nil)
}

// DownloadSpeed answers the client's overall download speed.
func (c Client) DownloadSpeed(ctx context.Context) (int64, error) {
	var stats struct {
		DownloadSpeed int64 `json:"downloadSpeed"`
	}
	err := c.call(ctx, "session-stats", nil, &stats)
	return stats.DownloadSpeed, err
}
