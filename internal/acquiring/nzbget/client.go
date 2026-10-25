// Package nzbget is the JSON-RPC client of NZBGet behind download.UsenetClient (ADR-46): Solarr uploads the NZB it
// fetched itself and tracks the job by its NZBID, in the queue and then in the history.
package nzbget

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

// Client is a thin JSON-RPC client over one connection; credentials are "user:password" (NZBGet's ControlUsername
// and ControlPassword).
type Client struct {
	http     *httpx.Client
	rpcURL   string
	username string
	password string
}

// NewClient builds one over an already-configured httpx.Client.
func NewClient(client *httpx.Client, baseURL, credentials string) Client {
	user, password, _ := strings.Cut(credentials, ":")
	return Client{http: client, rpcURL: strings.TrimRight(baseURL, "/") + "/jsonrpc", username: user, password: password}
}

type rpcRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c Client) call(ctx context.Context, method string, params []any, into any) error {
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(rpcRequest{Method: method, Params: params})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.SetBasicAuth(c.username, c.password)
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("nzbget: read %s: %w", method, err)
	}
	var parsed rpcResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return fmt.Errorf("nzbget: decode %s: %w", method, err)
	}
	if parsed.Error != nil {
		return fmt.Errorf("nzbget: %s: %s", method, parsed.Error.Message)
	}
	if into == nil {
		return nil
	}
	if err := json.Unmarshal(parsed.Result, into); err != nil {
		return fmt.Errorf("nzbget: decode %s: %w", method, err)
	}
	return nil
}

// Version answers NZBGet's version; every call needs the login, so it proves it too.
func (c Client) Version(ctx context.Context) (string, error) {
	var version string
	err := c.call(ctx, "version", nil, &version)
	return version, err
}

// Config answers NZBGet's settings by name.
func (c Client) Config(ctx context.Context) (map[string]string, error) {
	var options []struct {
		Name  string `json:"Name"`
		Value string `json:"Value"`
	}
	if err := c.call(ctx, "config", nil, &options); err != nil {
		return nil, err
	}
	config := make(map[string]string, len(options))
	for _, option := range options {
		config[option.Name] = option.Value
	}
	return config, nil
}

// Append uploads one NZB under category and answers its NZBID; 0 or less is NZBGet refusing it. This is the
// ten-argument form of NZBGet 16 and later.
func (c Client) Append(ctx context.Context, name string, nzb []byte, category string) (int64, error) {
	params := []any{
		name, base64.StdEncoding.EncodeToString(nzb), category, 0, false, false, "", 0, "SCORE", []any{},
	}
	var id int64
	err := c.call(ctx, "append", params, &id)
	return id, err
}

// size joins one of NZBGet's split 64-bit sizes.
func size(low, high int64) int64 { return high<<32 | (low & 0xffffffff) }

// Group is one job in the queue.
type Group struct {
	NZBID            int64  `json:"NZBID"`
	NZBName          string `json:"NZBName"`
	Category         string `json:"Category"`
	Status           string `json:"Status"`
	FileSizeLo       int64  `json:"FileSizeLo"`
	FileSizeHi       int64  `json:"FileSizeHi"`
	RemainingSizeLo  int64  `json:"RemainingSizeLo"`
	RemainingSizeHi  int64  `json:"RemainingSizeHi"`
	PausedSizeLo     int64  `json:"PausedSizeLo"`
	PausedSizeHi     int64  `json:"PausedSizeHi"`
	ActiveDownloads  int    `json:"ActiveDownloads"`
	DownloadedSizeMB int64  `json:"DownloadedSizeMB"`
}

// Groups answers the queue.
func (c Client) Groups(ctx context.Context) ([]Group, error) {
	var groups []Group
	err := c.call(ctx, "listgroups", []any{0}, &groups)
	return groups, err
}

// HistoryItem is one finished job.
type HistoryItem struct {
	NZBID      int64  `json:"NZBID"`
	Name       string `json:"Name"`
	Category   string `json:"Category"`
	Status     string `json:"Status"`
	DestDir    string `json:"DestDir"`
	FinalDir   string `json:"FinalDir"`
	FileSizeLo int64  `json:"FileSizeLo"`
	FileSizeHi int64  `json:"FileSizeHi"`
	Kind       string `json:"Kind"`
}

// History answers finished jobs, hidden ones left out.
func (c Client) History(ctx context.Context) ([]HistoryItem, error) {
	var items []HistoryItem
	err := c.call(ctx, "history", []any{false}, &items)
	return items, err
}

// Status is the part of the server status the queue totals need.
type Status struct {
	RemainingSizeLo int64 `json:"RemainingSizeLo"`
	RemainingSizeHi int64 `json:"RemainingSizeHi"`
	DownloadRate    int64 `json:"DownloadRate"`
	DownloadPaused  bool  `json:"DownloadPaused"`
}

// Status answers NZBGet's own status.
func (c Client) Status(ctx context.Context) (Status, error) {
	var status Status
	err := c.call(ctx, "status", nil, &status)
	return status, err
}

// EditQueue runs one editqueue command on jobs by NZBID; NZBGet answers false when it could not.
func (c Client) EditQueue(ctx context.Context, command string, ids []int64) error {
	var done bool
	if err := c.call(ctx, "editqueue", []any{command, "", ids}, &done); err != nil {
		return err
	}
	if !done {
		return fmt.Errorf("nzbget: %s of %v was refused", command, ids)
	}
	return nil
}

// parseID reads a client reference back into an NZBID.
func parseID(clientRef string) (int64, error) {
	id, err := strconv.ParseInt(clientRef, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("nzbget: %q is not an NZBID", clientRef)
	}
	return id, nil
}
