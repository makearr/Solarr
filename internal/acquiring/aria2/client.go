// Package aria2 is the JSON-RPC client of aria2 behind download.TorrentClient (ADR-46). aria2 tracks downloads by
// its own gid; Solarr keeps the info hash, as Sonarr does, and finds the gid by it.
package aria2

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 8 << 20

// DefaultURLBase is where aria2 serves its RPC.
const DefaultURLBase = "/rpc"

// stoppedLimit bounds how many finished downloads one listing reads.
const stoppedLimit = 1000

// Client is a thin JSON-RPC client over one connection; secret is aria2's rpc-secret, or empty.
type Client struct {
	http   *httpx.Client
	rpcURL string
	secret string
}

// NewClient builds one; urlBase is the RPC's path, empty for DefaultURLBase.
func NewClient(client *httpx.Client, baseURL, urlBase, secret string) Client {
	if strings.TrimSpace(urlBase) == "" {
		urlBase = DefaultURLBase
	}
	return Client{http: client, rpcURL: strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(urlBase, "/"), secret: secret}
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      string `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call performs one method, the secret first as aria2 wants it.
func (c Client) call(ctx context.Context, method string, params []any, into any) error {
	withSecret := []any{}
	if c.secret != "" {
		withSecret = append(withSecret, "token:"+c.secret)
	}
	payload, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: "solarr", Method: method, Params: append(withSecret, params...)})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("aria2: read %s: %w", method, err)
	}
	var parsed rpcResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("aria2: decode %s: %w", method, err)
	}
	if parsed.Error != nil {
		return fmt.Errorf("aria2: %s: %s", method, parsed.Error.Message)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(parsed.Result, into); err != nil {
		return fmt.Errorf("aria2: decode %s: %w", method, err)
	}
	return nil
}

// Version answers aria2's version; with a secret set it proves the secret too.
func (c Client) Version(ctx context.Context) (string, error) {
	var parsed struct {
		Version string `json:"version"`
	}
	err := c.call(ctx, "aria2.getVersion", nil, &parsed)
	return parsed.Version, err
}

// Directory answers where aria2 saves downloads.
func (c Client) Directory(ctx context.Context) (string, error) {
	var options struct {
		Dir string `json:"dir"`
	}
	err := c.call(ctx, "aria2.getGlobalOption", nil, &options)
	return options.Dir, err
}

// AddTorrent sends a torrent file and answers its gid.
func (c Client) AddTorrent(ctx context.Context, file []byte) (string, error) {
	var gid string
	err := c.call(ctx, "aria2.addTorrent", []any{base64.StdEncoding.EncodeToString(file), []string{}, map[string]string{}}, &gid)
	return gid, err
}

// AddURI sends a magnet link and answers its gid.
func (c Client) AddURI(ctx context.Context, magnet string) (string, error) {
	var gid string
	err := c.call(ctx, "aria2.addUri", []any{[]string{magnet}, map[string]string{}}, &gid)
	return gid, err
}

// Status is one download as tellActive, tellWaiting and tellStopped answer it; aria2 sends numbers as strings.
type Status struct {
	GID             string `json:"gid"`
	Status          string `json:"status"`
	TotalLength     string `json:"totalLength"`
	CompletedLength string `json:"completedLength"`
	DownloadSpeed   string `json:"downloadSpeed"`
	Dir             string `json:"dir"`
	InfoHash        string `json:"infoHash"`
	ErrorMessage    string `json:"errorMessage"`
	BitTorrent      struct {
		Info struct {
			Name string `json:"name"`
		} `json:"info"`
	} `json:"bittorrent"`
}

// Number reads one of aria2's string numbers; anything unreadable is 0.
func Number(text string) int64 {
	value, _ := strconv.ParseInt(text, 10, 64)
	return value
}

var statusKeys = []string{
	"gid", "status", "totalLength", "completedLength", "downloadSpeed", "dir", "infoHash", "errorMessage", "bittorrent",
}

// Downloads answers every download aria2 holds: active, waiting and stopped.
func (c Client) Downloads(ctx context.Context) ([]Status, error) {
	var all []Status
	for _, query := range []struct {
		method string
		params []any
	}{
		{"aria2.tellActive", []any{statusKeys}},
		{"aria2.tellWaiting", []any{0, stoppedLimit, statusKeys}},
		{"aria2.tellStopped", []any{0, stoppedLimit, statusKeys}},
	} {
		var page []Status
		if err := c.call(ctx, query.method, query.params, &page); err != nil {
			return nil, err
		}
		all = append(all, page...)
	}
	return all, nil
}

// Act runs one method on one download by gid: forceRemove, removeDownloadResult, forcePause, unpause.
func (c Client) Act(ctx context.Context, method, gid string) error {
	return c.call(ctx, "aria2."+method, []any{gid}, nil)
}

// DownloadSpeed answers the overall download speed.
func (c Client) DownloadSpeed(ctx context.Context) (int64, error) {
	var stat struct {
		DownloadSpeed string `json:"downloadSpeed"`
	}
	err := c.call(ctx, "aria2.getGlobalStat", nil, &stat)
	return Number(stat.DownloadSpeed), err
}
