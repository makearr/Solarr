// Package deluge is the web UI JSON-RPC client of Deluge behind download.TorrentClient (ADR-46): Solarr signs in with
// the web UI's password, connects it to its daemon when it is not, and labels torrents with the Label plugin.
package deluge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 8 << 20

// ErrLoginRefused is Deluge refusing the web UI password.
var ErrLoginRefused = errors.New("deluge: the password was refused")

// ErrNoDaemon is a web UI that has no daemon to connect to.
var ErrNoDaemon = errors.New("deluge: the web UI is connected to no daemon; add one in its Connection Manager")

// Client is a thin JSON-RPC client over one connection; credentials is the web UI's password.
type Client struct {
	http     *httpx.Client
	rpcURL   string
	password string
	session  *string
}

// NewClient builds one over an already-configured httpx.Client.
func NewClient(client *httpx.Client, baseURL, password string) Client {
	return Client{http: client, rpcURL: strings.TrimRight(baseURL, "/") + "/json", password: password, session: new(string)}
}

type rpcRequest struct {
	Method string `json:"method"`
	Params []any  `json:"params"`
	ID     int    `json:"id"`
}

type rpcError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

// notAuthenticated is the error code Deluge answers a call without a session with.
const notAuthenticated = 1

// MethodError is Deluge refusing one call.
type MethodError struct {
	Method  string
	Message string
}

func (e *MethodError) Error() string { return fmt.Sprintf("deluge: %s: %s", e.Method, e.Message) }

func (c Client) send(ctx context.Context, method string, params []any) (rpcResponse, error) {
	if params == nil {
		params = []any{}
	}
	payload, err := json.Marshal(rpcRequest{Method: method, Params: params, ID: 1})
	if err != nil {
		return rpcResponse{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.rpcURL, bytes.NewReader(payload))
	if err != nil {
		return rpcResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	if *c.session != "" {
		request.AddCookie(&http.Cookie{Name: "_session_id", Value: *c.session})
	}
	response, err := c.http.Do(ctx, request)
	if err != nil {
		return rpcResponse{}, err
	}
	defer func() { _ = response.Body.Close() }()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "_session_id" {
			*c.session = cookie.Value
		}
	}
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return rpcResponse{}, fmt.Errorf("deluge: read %s: %w", method, err)
	}
	var parsed rpcResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return rpcResponse{}, fmt.Errorf("deluge: decode %s: %w", method, err)
	}
	return parsed, nil
}

// call performs one method, signing in and connecting the daemon first when Deluge says it is needed.
func (c Client) call(ctx context.Context, method string, params []any, into any) error {
	return c.do(ctx, method, params, into, true)
}

func (c Client) do(ctx context.Context, method string, params []any, into any, mayLogin bool) error {
	for attempt := 0; ; attempt++ {
		parsed, err := c.send(ctx, method, params)
		if err != nil {
			return err
		}
		if parsed.Error != nil && parsed.Error.Code == notAuthenticated && mayLogin && attempt == 0 {
			if err := c.login(ctx); err != nil {
				return err
			}
			continue
		}
		if parsed.Error != nil {
			return &MethodError{Method: method, Message: parsed.Error.Message}
		}
		if into == nil {
			return nil
		}
		if err := json.Unmarshal(parsed.Result, into); err != nil {
			return fmt.Errorf("deluge: decode %s: %w", method, err)
		}
		return nil
	}
}

func (c Client) login(ctx context.Context) error {
	parsed, err := c.send(ctx, "auth.login", []any{c.password})
	if err != nil {
		return err
	}
	var accepted bool
	if parsed.Error != nil || json.Unmarshal(parsed.Result, &accepted) != nil || !accepted {
		return ErrLoginRefused
	}
	return c.connect(ctx)
}

// connect attaches the web UI to its first daemon, as Sonarr does, when it is attached to none.
func (c Client) connect(ctx context.Context) error {
	var connected bool
	if err := c.do(ctx, "web.connected", nil, &connected, false); err != nil || connected {
		return err
	}
	var hosts [][]json.RawMessage
	if err := c.do(ctx, "web.get_hosts", nil, &hosts, false); err != nil {
		return err
	}
	if len(hosts) == 0 || len(hosts[0]) == 0 {
		return ErrNoDaemon
	}
	var hostID string
	if err := json.Unmarshal(hosts[0][0], &hostID); err != nil {
		return fmt.Errorf("deluge: read daemon id: %w", err)
	}
	return c.do(ctx, "web.connect", []any{hostID}, nil, false)
}

// Version answers the daemon's version; it needs the login, so it proves it too.
func (c Client) Version(ctx context.Context) (string, error) {
	var version string
	err := c.call(ctx, "daemon.info", nil, &version)
	return version, err
}

// Config is the part of the daemon's settings that says where finished torrents are.
type Config struct {
	DownloadLocation  string `json:"download_location"`
	MoveCompleted     bool   `json:"move_completed"`
	MoveCompletedPath string `json:"move_completed_path"`
}

// FinishedFolder is where a finished torrent's files are.
func (c Config) FinishedFolder() string {
	if c.MoveCompleted && c.MoveCompletedPath != "" {
		return c.MoveCompletedPath
	}
	return c.DownloadLocation
}

// Config answers the daemon's settings.
func (c Client) Config(ctx context.Context) (Config, error) {
	var config Config
	err := c.call(ctx, "core.get_config", nil, &config)
	return config, err
}

// addOptions are the options a torrent is added with; Deluge's defaults apply to the rest.
type addOptions struct {
	AddPaused bool `json:"add_paused"`
}

// AddFile sends a torrent file and answers its hash; Deluge answers none for a torrent it already has.
func (c Client) AddFile(ctx context.Context, name string, base64File string) (string, error) {
	var hash *string
	if err := c.call(ctx, "core.add_torrent_file", []any{name, base64File, addOptions{}}, &hash); err != nil {
		return "", err
	}
	if hash == nil {
		return "", nil
	}
	return *hash, nil
}

// AddMagnet sends a magnet link and answers its hash.
func (c Client) AddMagnet(ctx context.Context, magnet string) (string, error) {
	var hash *string
	if err := c.call(ctx, "core.add_torrent_magnet", []any{magnet, addOptions{}}, &hash); err != nil {
		return "", err
	}
	if hash == nil {
		return "", nil
	}
	return *hash, nil
}

// Labels answers the Label plugin's labels; a MethodError when the plugin is not enabled.
func (c Client) Labels(ctx context.Context) ([]string, error) {
	var labels []string
	err := c.call(ctx, "label.get_labels", nil, &labels)
	return labels, err
}

// SetLabel files a torrent under label, creating the label first when Deluge does not have it.
func (c Client) SetLabel(ctx context.Context, hash, label string) error {
	labels, err := c.Labels(ctx)
	if err != nil {
		return err
	}
	label = strings.ToLower(label)
	found := false
	for _, existing := range labels {
		found = found || existing == label
	}
	if !found {
		if err := c.call(ctx, "label.add", []any{label}, nil); err != nil {
			return err
		}
	}
	return c.call(ctx, "label.set_torrent", []any{hash, label}, nil)
}

// Torrent is the part of a torrent's status one download needs.
type Torrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	State       string  `json:"state"`
	Label       string  `json:"label"`
	TotalSize   int64   `json:"total_size"`
	TotalDone   int64   `json:"total_done"`
	SavePath    string  `json:"save_path"`
	IsFinished  bool    `json:"is_finished"`
	Ratio       float64 `json:"ratio"`
	SeedingTime int64   `json:"seeding_time"`
	Message     string  `json:"message"`
	Rate        float64 `json:"download_payload_rate"`
}

var torrentFields = []string{
	"hash", "name", "state", "label", "total_size", "total_done", "save_path", "is_finished", "ratio", "seeding_time",
	"message", "download_payload_rate",
}

// Torrents answers every torrent.
func (c Client) Torrents(ctx context.Context) ([]Torrent, error) {
	var byHash map[string]Torrent
	if err := c.call(ctx, "core.get_torrents_status", []any{map[string]string{}, torrentFields}, &byHash); err != nil {
		return nil, err
	}
	torrents := make([]Torrent, 0, len(byHash))
	for hash, torrent := range byHash {
		torrent.Hash = hash
		torrents = append(torrents, torrent)
	}
	slices.SortFunc(torrents, func(a, b Torrent) int { return strings.Compare(a.Hash, b.Hash) })
	return torrents, nil
}

// Remove deletes one torrent; removeData also deletes what it downloaded.
func (c Client) Remove(ctx context.Context, hash string, removeData bool) error {
	return c.call(ctx, "core.remove_torrent", []any{hash, removeData}, nil)
}

// SetPaused pauses or resumes torrents; Deluge 2 takes a list as core.pause_torrents, Deluge 1 as core.pause_torrent.
func (c Client) SetPaused(ctx context.Context, hashes []string, paused bool) error {
	method := "core.resume_torrent"
	if paused {
		method = "core.pause_torrent"
	}
	err := c.call(ctx, method+"s", []any{hashes}, nil)
	var refused *MethodError
	if errors.As(err, &refused) && strings.Contains(strings.ToLower(refused.Message), "unknown method") {
		return c.call(ctx, method, []any{hashes}, nil)
	}
	return err
}

// DownloadRate answers the daemon's overall download speed.
func (c Client) DownloadRate(ctx context.Context) (int64, error) {
	var status struct {
		Rate float64 `json:"payload_download_rate"`
	}
	err := c.call(ctx, "core.get_session_status", []any{[]string{"payload_download_rate"}}, &status)
	return int64(status.Rate), err
}
