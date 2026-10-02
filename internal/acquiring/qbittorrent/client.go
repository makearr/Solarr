// Package qbittorrent is the Web API v2 client of qBittorrent behind download.Client (ADR-46): Solarr sends a
// torrent, watches it by info hash and never touches the client's other torrents. As with sabnzbd, the caller
// supplies an already-configured httpx.Client.
package qbittorrent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 8 << 20

// ErrLoginRefused is qBittorrent refusing the user name or password.
var ErrLoginRefused = errors.New("qbittorrent: the user name or password was refused")

// ErrForbidden is qBittorrent answering 403: it wants a login, and none was configured or it expired.
var ErrForbidden = errors.New("qbittorrent: access refused; set a user name and password")

// Client is a thin Web API client over one downloader connection. A host that whitelists Solarr's address needs
// no credentials; otherwise credentials are "user:password".
type Client struct {
	http     *httpx.Client
	baseURL  string
	username string
	password string
	session  *string
	// sessionName is the cookie qBittorrent named the session with.
	sessionName *string
}

// NewClient builds one; credentials is "user:password", or empty when the host lets this address in.
func NewClient(client *httpx.Client, baseURL, credentials string) Client {
	user, password, _ := strings.Cut(credentials, ":")
	return Client{http: client, baseURL: strings.TrimRight(baseURL, "/"), username: user, password: password, session: new(string), sessionName: new(string)}
}

func (c Client) login(ctx context.Context) error {
	form := url.Values{"username": {c.username}, "password": {c.password}}
	_, cookies, body, err := c.send(ctx, http.MethodPost, "/api/v2/auth/login", "application/x-www-form-urlencoded", strings.NewReader(form.Encode()), false)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(body)) != "Ok." {
		return ErrLoginRefused
	}
	for _, cookie := range cookies {
		// "SID", or "QBT_SID_<port>" since v5.
		if cookie.Name == "SID" || strings.HasPrefix(cookie.Name, "QBT_SID") {
			*c.sessionName = cookie.Name
			*c.session = cookie.Value
		}
	}
	return nil
}

func (c Client) send(
	ctx context.Context, method, path, contentType string, body io.Reader, withSession bool,
) (status int, cookies []*http.Cookie, data []byte, err error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return 0, nil, nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	// qBittorrent checks Referer/Origin against the host on every state-changing call.
	request.Header.Set("Referer", c.baseURL)
	if withSession && *c.session != "" {
		request.AddCookie(&http.Cookie{Name: *c.sessionName, Value: *c.session})
	}
	response, err := c.http.Do(ctx, request)
	// httpx turns a non-success status into an error; a 403 or 404 is an answer here (login needed, an older API).
	var refused *httpx.StatusError
	if errors.As(err, &refused) && refused.Class == httpx.ClassTerminal {
		return refused.Status, nil, nil, nil
	}
	if err != nil {
		return 0, nil, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err = io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, nil, fmt.Errorf("qbittorrent: read response: %w", err)
	}
	return response.StatusCode, response.Cookies(), data, nil
}

// call performs one request, logging in first when credentials are set and again once on a 403.
func (c Client) call(ctx context.Context, method, path, contentType string, payload []byte) (int, []byte, error) {
	if c.username != "" && *c.session == "" {
		if err := c.login(ctx); err != nil {
			return 0, nil, err
		}
	}
	for attempt := 0; ; attempt++ {
		status, _, data, err := c.send(ctx, method, path, contentType, bytes.NewReader(payload), true)
		if err != nil {
			return 0, nil, err
		}
		if status != http.StatusForbidden {
			return status, data, nil
		}
		if c.username == "" || attempt > 0 {
			return status, data, ErrForbidden
		}
		if err := c.login(ctx); err != nil {
			return 0, nil, err
		}
	}
}

func (c Client) getJSON(ctx context.Context, path string, query url.Values, into any) error {
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	status, data, err := c.call(ctx, http.MethodGet, target, "", nil)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("qbittorrent: %s answered %d", path, status)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("qbittorrent: decode %s: %w", path, err)
	}
	return nil
}

func (c Client) postForm(ctx context.Context, path string, form url.Values) (int, []byte, error) {
	return c.call(ctx, http.MethodPost, path, "application/x-www-form-urlencoded", []byte(form.Encode()))
}

// Version answers qBittorrent's own version; with credentials it proves them too.
func (c Client) Version(ctx context.Context) (string, error) {
	status, data, err := c.call(ctx, http.MethodGet, "/api/v2/app/version", "", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("qbittorrent: app/version answered %d", status)
	}
	return strings.TrimSpace(string(data)), nil
}

// Categories answers the names of the categories qBittorrent has.
func (c Client) Categories(ctx context.Context) ([]string, error) {
	var categories map[string]json.RawMessage
	if err := c.getJSON(ctx, "/api/v2/torrents/categories", nil, &categories); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(categories))
	for name := range categories {
		names = append(names, name)
	}
	return names, nil
}

// Torrent is the part of qBittorrent's torrents/info one download needs.
type Torrent struct {
	Hash        string  `json:"hash"`
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	State       string  `json:"state"`
	Size        int64   `json:"size"`
	AmountLeft  int64   `json:"amount_left"`
	ContentPath string  `json:"content_path"`
	DLSpeed     int64   `json:"dlspeed"`
	Progress    float64 `json:"progress"`
	Ratio       float64 `json:"ratio"`
	SeedingTime int64   `json:"seeding_time"`
}

// Torrents answers the torrents in category, or every torrent for "".
func (c Client) Torrents(ctx context.Context, category string) ([]Torrent, error) {
	query := url.Values{}
	if category != "" {
		query.Set("category", category)
	}
	var torrents []Torrent
	if err := c.getJSON(ctx, "/api/v2/torrents/info", query, &torrents); err != nil {
		return nil, err
	}
	return torrents, nil
}

// AddRequest is one torrent file, or a magnet link in Magnet, sent under a category.
type AddRequest struct {
	File     []byte
	Magnet   string
	Category string
}

// Add sends the torrent. qBittorrent answers "Ok." or "Fails." with no reason (a torrent already added is the usual one).
func (c Client) Add(ctx context.Context, request AddRequest) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if request.Category != "" {
		_ = writer.WriteField("category", request.Category)
	}
	// A single-file torrent would otherwise be a bare file; import takes a folder.
	_ = writer.WriteField("contentLayout", "Subfolder")
	if request.Magnet != "" {
		_ = writer.WriteField("urls", request.Magnet)
	} else {
		part, err := writer.CreateFormFile("torrents", "release.torrent")
		if err != nil {
			return err
		}
		if _, err := part.Write(request.File); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	status, data, err := c.call(ctx, http.MethodPost, "/api/v2/torrents/add", writer.FormDataContentType(), body.Bytes())
	if err != nil {
		return err
	}
	if status != http.StatusOK || !addSucceeded(data) {
		return fmt.Errorf("qbittorrent: add torrent: %w", ErrRefused)
	}
	return nil
}

// addSucceeded reads torrents/add's answer: "Ok." or "Fails." before v5.2, a JSON count of what was added, still
// loading its metadata, or failed after. Anything unreadable is taken as success, since the status was 200.
func addSucceeded(body []byte) bool {
	text := strings.TrimSpace(string(body))
	if text == "Fails." {
		return false
	}
	var counts struct {
		Failure *int `json:"failure_count"`
	}
	if json.Unmarshal(body, &counts) == nil && counts.Failure != nil {
		return *counts.Failure == 0
	}
	return true
}

// ErrRefused is qBittorrent declining a torrent it was sent.
var ErrRefused = errors.New("qbittorrent refused the torrent")

// Delete removes torrents by hash; deleteFiles also deletes what they downloaded.
func (c Client) Delete(ctx context.Context, hashes []string, deleteFiles bool) error {
	status, _, err := c.postForm(ctx, "/api/v2/torrents/delete", url.Values{
		"hashes": {strings.Join(hashes, "|")}, "deleteFiles": {fmt.Sprint(deleteFiles)},
	})
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("qbittorrent: delete answered %d", status)
	}
	return nil
}

// SetPaused pauses or resumes torrents by hash; v5 renamed pause/resume to stop/start, so the old names are the fallback.
func (c Client) SetPaused(ctx context.Context, hashes []string, paused bool) error {
	names := [2]string{"resume", "start"}
	if paused {
		names = [2]string{"pause", "stop"}
	}
	form := url.Values{"hashes": {strings.Join(hashes, "|")}}
	status, _, err := c.postForm(ctx, "/api/v2/torrents/"+names[1], form)
	if err != nil {
		return err
	}
	if status == http.StatusNotFound {
		status, _, err = c.postForm(ctx, "/api/v2/torrents/"+names[0], form)
		if err != nil {
			return err
		}
	}
	if status != http.StatusOK {
		return fmt.Errorf("qbittorrent: pause/resume answered %d", status)
	}
	return nil
}

// TransferInfo is the part of transfer/info the queue status needs.
type TransferInfo struct {
	DLSpeed int64 `json:"dl_info_speed"`
}

// Transfer answers the global transfer figures.
func (c Client) Transfer(ctx context.Context) (TransferInfo, error) {
	var info TransferInfo
	err := c.getJSON(ctx, "/api/v2/transfer/info", nil, &info)
	return info, err
}

// SavePath answers qBittorrent's default save path, where a finished torrent's files are as the client names it.
func (c Client) SavePath(ctx context.Context) (string, error) {
	var preferences struct {
		SavePath string `json:"save_path"`
	}
	if err := c.getJSON(ctx, "/api/v2/app/preferences", nil, &preferences); err != nil {
		return "", err
	}
	return preferences.SavePath, nil
}
