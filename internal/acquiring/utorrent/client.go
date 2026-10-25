// Package utorrent is the WebUI client of µTorrent behind download.TorrentClient (ADR-46): every call carries the
// token token.html hands out with its GUID cookie, and a torrent's label is its category.
package utorrent

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
	"regexp"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// maxResponseBytes bounds what one call can make this process read.
const maxResponseBytes = 8 << 20

// tokenPattern finds the token in token.html: <div id='token' style='display:none;'>TOKEN</div>.
var tokenPattern = regexp.MustCompile(`<div[^>]*id=['"]token['"][^>]*>([^<]+)</div>`)

// ErrNoToken is a WebUI that answered token.html without a token: not µTorrent, or its WebUI is off.
var ErrNoToken = errors.New("utorrent: the WebUI handed out no token")

// Client is a thin WebUI client over one connection; credentials are "user:password".
type Client struct {
	http     *httpx.Client
	guiURL   string
	username string
	password string
	token    *string
	cookie   *string
}

// NewClient builds one over an already-configured httpx.Client.
func NewClient(client *httpx.Client, baseURL, credentials string) Client {
	user, password, _ := strings.Cut(credentials, ":")
	return Client{
		http: client, guiURL: strings.TrimRight(baseURL, "/") + "/gui/", username: user, password: password,
		token: new(string), cookie: new(string),
	}
}

func (c Client) send(ctx context.Context, method, target, contentType string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	request.SetBasicAuth(c.username, c.password)
	if *c.cookie != "" {
		request.AddCookie(&http.Cookie{Name: "GUID", Value: *c.cookie})
	}
	return c.http.Do(ctx, request)
}

func (c Client) authenticate(ctx context.Context) error {
	response, err := c.send(ctx, http.MethodGet, c.guiURL+"token.html", "", nil)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	for _, cookie := range response.Cookies() {
		if cookie.Name == "GUID" {
			*c.cookie = cookie.Value
		}
	}
	body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
	if err != nil {
		return fmt.Errorf("utorrent: read token: %w", err)
	}
	match := tokenPattern.FindSubmatch(body)
	if match == nil {
		return ErrNoToken
	}
	*c.token = strings.TrimSpace(string(match[1]))
	return nil
}

// call performs one action with the token, fetching a fresh one once when µTorrent answers 400 to a stale one.
func (c Client) call(ctx context.Context, query url.Values, file []byte, into any) error {
	if *c.token == "" {
		if err := c.authenticate(ctx); err != nil {
			return err
		}
	}
	for attempt := 0; ; attempt++ {
		withToken := url.Values{"token": {*c.token}}
		for key, values := range query {
			withToken[key] = values
		}
		target := c.guiURL + "?" + withToken.Encode()
		var response *http.Response
		var err error
		if file == nil {
			response, err = c.send(ctx, http.MethodGet, target, "", nil)
		} else {
			response, err = c.sendFile(ctx, target, file)
		}
		var stale *httpx.StatusError
		if errors.As(err, &stale) && stale.Status == http.StatusBadRequest && attempt == 0 {
			if err := c.authenticate(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		body, err := httpx.ReadLimited(response.Body, maxResponseBytes)
		_ = response.Body.Close()
		if err != nil {
			return fmt.Errorf("utorrent: read response: %w", err)
		}
		if into == nil {
			return nil
		}
		if err := json.Unmarshal(body, into); err != nil {
			return fmt.Errorf("utorrent: decode response: %w", err)
		}
		return nil
	}
}

func (c Client) sendFile(ctx context.Context, target string, file []byte) (*http.Response, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("torrent_file", "release.torrent")
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, bytes.NewReader(file)); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return c.send(ctx, http.MethodPost, target, writer.FormDataContentType(), body.Bytes())
}

// Torrent is one row of list=1, by µTorrent's column order.
type Torrent struct {
	Hash         string
	Status       int64
	Name         string
	Size         int64
	ProgressPerm int64
	RatioPerm    int64
	DownSpeed    int64
	Label        string
	Remaining    int64
	StatusText   string
	SavePath     string
}

// List is list=1's answer: the build, every torrent and the labels.
type List struct {
	Build    int64
	Torrents []Torrent
	Labels   []string
}

// List answers every torrent and label.
func (c Client) List(ctx context.Context) (List, error) {
	var parsed struct {
		Build    int64               `json:"build"`
		Torrents [][]json.RawMessage `json:"torrents"`
		Labels   [][]json.RawMessage `json:"label"`
	}
	if err := c.call(ctx, url.Values{"list": {"1"}}, nil, &parsed); err != nil {
		return List{}, err
	}
	list := List{Build: parsed.Build}
	for _, row := range parsed.Torrents {
		if len(row) < 27 {
			return List{}, fmt.Errorf("utorrent: a torrent row has %d columns, want 27", len(row))
		}
		var torrent Torrent
		for _, column := range []struct {
			index int
			into  any
		}{
			{0, &torrent.Hash}, {1, &torrent.Status}, {2, &torrent.Name}, {3, &torrent.Size}, {4, &torrent.ProgressPerm},
			{7, &torrent.RatioPerm}, {9, &torrent.DownSpeed}, {11, &torrent.Label}, {18, &torrent.Remaining},
			{21, &torrent.StatusText}, {26, &torrent.SavePath},
		} {
			if err := json.Unmarshal(row[column.index], column.into); err != nil {
				return List{}, fmt.Errorf("utorrent: decode column %d: %w", column.index, err)
			}
		}
		list.Torrents = append(list.Torrents, torrent)
	}
	for _, label := range parsed.Labels {
		var name string
		if len(label) > 0 && json.Unmarshal(label[0], &name) == nil {
			list.Labels = append(list.Labels, name)
		}
	}
	return list, nil
}

// AddFile uploads a torrent file.
func (c Client) AddFile(ctx context.Context, file []byte) error {
	return c.call(ctx, url.Values{"action": {"add-file"}}, file, nil)
}

// AddURL adds a magnet link.
func (c Client) AddURL(ctx context.Context, magnet string) error {
	return c.call(ctx, url.Values{"action": {"add-url"}, "s": {magnet}}, nil, nil)
}

// SetLabel files one torrent under label.
func (c Client) SetLabel(ctx context.Context, hash, label string) error {
	return c.call(ctx, url.Values{"action": {"setprops"}, "hash": {hash}, "s": {"label"}, "v": {label}}, nil, nil)
}

// Act runs one action ("remove", "removedata", "stop", "start") on torrents by hash.
func (c Client) Act(ctx context.Context, action string, hashes []string) error {
	return c.call(ctx, url.Values{"action": {action}, "hash": hashes}, nil, nil)
}

// Settings answers µTorrent's settings by name.
func (c Client) Settings(ctx context.Context) (map[string]string, error) {
	var parsed struct {
		Settings [][]json.RawMessage `json:"settings"`
	}
	if err := c.call(ctx, url.Values{"action": {"getsettings"}}, nil, &parsed); err != nil {
		return nil, err
	}
	settings := make(map[string]string, len(parsed.Settings))
	for _, setting := range parsed.Settings {
		if len(setting) < 3 {
			continue
		}
		var name string
		if json.Unmarshal(setting[0], &name) != nil {
			continue
		}
		var text string
		if json.Unmarshal(setting[2], &text) != nil {
			text = string(setting[2])
		}
		settings[name] = text
	}
	return settings, nil
}
