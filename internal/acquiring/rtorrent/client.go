// Package rtorrent is the XML-RPC client of rTorrent (and ruTorrent's RPC endpoint) behind download.TorrentClient
// (ADR-46): a torrent's label is its custom1, as ruTorrent and Sonarr both keep it.
package rtorrent

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 8 << 20

// DefaultURLBase is where rTorrent's SCGI bridge usually serves XML-RPC.
const DefaultURLBase = "RPC2"

// Client is a thin XML-RPC client over one connection; credentials are "user:password" for HTTP basic auth, or empty.
type Client struct {
	http     *httpx.Client
	rpcURL   string
	username string
	password string
}

// NewClient builds one; urlBase is the XML-RPC path, empty for DefaultURLBase.
func NewClient(client *httpx.Client, baseURL, urlBase, credentials string) Client {
	if strings.TrimSpace(urlBase) == "" {
		urlBase = DefaultURLBase
	}
	user, password, _ := strings.Cut(credentials, ":")
	return Client{
		http: client, rpcURL: strings.TrimRight(baseURL, "/") + "/" + strings.TrimLeft(urlBase, "/"),
		username: user, password: password,
	}
}

func (c Client) call(ctx context.Context, method string, params ...param) (value, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(encodeCall(method, params)))
	if err != nil {
		return value{}, err
	}
	request.Header.Set("Content-Type", "text/xml")
	if c.username != "" || c.password != "" {
		request.SetBasicAuth(c.username, c.password)
	}
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return value{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return value{}, fmt.Errorf("rtorrent: read %s: %w", method, err)
	}
	parsed, err := decodeResponse(body)
	if err != nil {
		return value{}, fmt.Errorf("rtorrent: %s: %w", method, err)
	}
	return parsed, nil
}

// Version answers rTorrent's version.
func (c Client) Version(ctx context.Context) (string, error) {
	answer, err := c.call(ctx, "system.client_version")
	return answer.text, err
}

// DefaultDirectory answers where rTorrent saves torrents unless told otherwise.
func (c Client) DefaultDirectory(ctx context.Context) (string, error) {
	answer, err := c.call(ctx, "directory.default", stringParam(""))
	return answer.text, err
}

// commands are what a torrent is added with: its label.
func commands(label string) []param {
	if label == "" {
		return nil
	}
	return []param{stringParam("d.custom1.set=" + label)}
}

// AddFile loads and starts a torrent file; rTorrent answers 0 for done.
func (c Client) AddFile(ctx context.Context, file []byte, label string) error {
	return c.load(ctx, "load.raw_start", bytesParam(file), label)
}

// AddMagnet loads and starts a magnet link.
func (c Client) AddMagnet(ctx context.Context, magnet, label string) error {
	return c.load(ctx, "load.start", stringParam(magnet), label)
}

func (c Client) load(ctx context.Context, method string, torrent param, label string) error {
	answer, err := c.call(ctx, method, append([]param{stringParam(""), torrent}, commands(label)...)...)
	if err != nil {
		return err
	}
	if answer.isInt && answer.integer != 0 {
		return fmt.Errorf("rtorrent: %s answered %d", method, answer.integer)
	}
	return nil
}

// Torrent is one row of d.multicall2.
type Torrent struct {
	Hash         string
	Name         string
	BasePath     string
	Directory    string
	Label        string
	SizeBytes    int64
	LeftBytes    int64
	DownRate     int64
	RatioPermill int64
	IsActive     bool
	IsComplete   bool
	IsOpen       bool
	Message      string
}

// torrentFields is the multicall's columns, in Torrent's order.
var torrentFields = []string{
	"d.hash=", "d.name=", "d.base_path=", "d.directory=", "d.custom1=", "d.size_bytes=", "d.left_bytes=",
	"d.down.rate=", "d.ratio=", "d.is_active=", "d.complete=", "d.is_open=", "d.message=",
}

// Torrents answers every torrent in the main view.
func (c Client) Torrents(ctx context.Context) ([]Torrent, error) {
	params := []param{stringParam(""), stringParam("main")}
	for _, field := range torrentFields {
		params = append(params, stringParam(field))
	}
	answer, err := c.call(ctx, "d.multicall2", params...)
	if err != nil {
		return nil, err
	}
	torrents := make([]Torrent, 0, len(answer.array))
	for _, row := range answer.array {
		if len(row.array) < len(torrentFields) {
			return nil, fmt.Errorf("rtorrent: d.multicall2 answered %d columns, want %d", len(row.array), len(torrentFields))
		}
		column := row.array
		torrents = append(torrents, Torrent{
			Hash: column[0].text, Name: column[1].text, BasePath: column[2].text, Directory: column[3].text,
			Label: column[4].text, SizeBytes: column[5].integer, LeftBytes: column[6].integer, DownRate: column[7].integer,
			RatioPermill: column[8].integer, IsActive: column[9].integer == 1, IsComplete: column[10].integer == 1,
			IsOpen: column[11].integer == 1, Message: column[12].text,
		})
	}
	return torrents, nil
}

// Erase removes one torrent. With deleteData it is first marked for ruTorrent's erasedata plugin (custom5=1), which
// deletes the files; rTorrent alone never deletes them.
func (c Client) Erase(ctx context.Context, hash string, deleteData bool) error {
	if deleteData {
		if _, err := c.call(ctx, "d.custom5.set", stringParam(hash), stringParam("1")); err != nil {
			return err
		}
	}
	_, err := c.call(ctx, "d.erase", stringParam(hash))
	return err
}

// SetStarted stops or starts one torrent.
func (c Client) SetStarted(ctx context.Context, hash string, started bool) error {
	method := "d.stop"
	if started {
		method = "d.start"
	}
	_, err := c.call(ctx, method, stringParam(hash))
	return err
}

// DownloadRate answers the overall download speed.
func (c Client) DownloadRate(ctx context.Context) (int64, error) {
	answer, err := c.call(ctx, "throttle.global_down.rate", stringParam(""))
	return answer.integer, err
}
