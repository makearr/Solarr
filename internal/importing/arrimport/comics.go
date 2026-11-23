package arrimport

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
)

// ComicSeries is one series of a comic manager (Kapowarr's volume, Mylar3's comic) as an import maps it: the ComicVine
// volume id is the identity both keep.
type ComicSeries struct {
	ID         string
	Title      string
	Year       int
	ComicVine  string
	Monitored  bool
	HasFiles   bool
	Folder     string
	RootFolder string
}

// KapowarrClient is the REST client for one Kapowarr instance: GET only, the key as the api_key query parameter.
type KapowarrClient struct{ inner LidarrClient }

// Kapowarr reads the same instance as a Kapowarr: the transport and key are shared, only the API differs.
func (c LidarrClient) Kapowarr() KapowarrClient { return KapowarrClient{inner: c} }

type kapowarrAnswer[T any] struct {
	Error  *string `json:"error"`
	Result T       `json:"result"`
}

func kapowarrGet[T any](ctx context.Context, c LidarrClient, endpoint string) (T, error) {
	var answer kapowarrAnswer[T]
	if err := c.getKeyed(ctx, endpoint, "api_key", nil, &answer); err != nil {
		return answer.Result, err
	}
	if answer.Error != nil && *answer.Error != "" {
		return answer.Result, fmt.Errorf("arrimport: %s: %s", endpoint, *answer.Error)
	}
	return answer.Result, nil
}

// SystemStatus is the connection test: Kapowarr's about page answers its version.
func (c KapowarrClient) SystemStatus(ctx context.Context) (LidarrStatus, error) {
	about, err := kapowarrGet[struct {
		Version string `json:"version"`
	}](ctx, c.inner, "/api/system/about")
	return LidarrStatus{Version: about.Version}, err
}

// RootFolders answers Kapowarr's root folders.
func (c KapowarrClient) RootFolders(ctx context.Context) ([]LidarrRootFolder, error) {
	found, err := kapowarrGet[[]struct {
		ID     int64  `json:"id"`
		Folder string `json:"folder"`
	}](ctx, c.inner, "/api/rootfolder")
	if err != nil {
		return nil, err
	}
	folders := make([]LidarrRootFolder, len(found))
	for index, folder := range found {
		folders[index] = LidarrRootFolder{ID: folder.ID, Path: folder.Folder}
	}
	return folders, nil
}

// Volumes answers every volume Kapowarr holds, with the root folder each lives in.
func (c KapowarrClient) Volumes(ctx context.Context) ([]ComicSeries, error) {
	roots, err := c.RootFolders(ctx)
	if err != nil {
		return nil, err
	}
	rootPath := make(map[int64]string, len(roots))
	for _, root := range roots {
		rootPath[root.ID] = root.Path
	}
	found, err := kapowarrGet[[]struct {
		ID               int64  `json:"id"`
		ComicVineID      int64  `json:"comicvine_id"`
		Title            string `json:"title"`
		Year             int    `json:"year"`
		Monitored        bool   `json:"monitored"`
		RootFolder       int64  `json:"root_folder"`
		Folder           string `json:"folder"`
		IssuesDownloaded int    `json:"issues_downloaded"`
	}](ctx, c.inner, "/api/volumes")
	if err != nil {
		return nil, err
	}
	series := make([]ComicSeries, len(found))
	for index, volume := range found {
		root := rootPath[volume.RootFolder]
		if root == "" {
			root = path.Dir(volume.Folder)
		}
		series[index] = ComicSeries{
			ID: strconv.FormatInt(volume.ID, 10), Title: volume.Title, Year: volume.Year, ComicVine: comicVineText(volume.ComicVineID),
			Monitored: volume.Monitored, HasFiles: volume.IssuesDownloaded > 0, Folder: volume.Folder, RootFolder: root,
		}
	}
	return series, nil
}

// MylarClient is the REST client for one Mylar3 instance: GET only, the key and command as query parameters of /api.
type MylarClient struct{ inner LidarrClient }

// Mylar reads the same instance as a Mylar3.
func (c LidarrClient) Mylar() MylarClient { return MylarClient{inner: c} }

// mylarList reads an answer that is the list itself or an object holding it under "data".
func mylarList(ctx context.Context, c LidarrClient, command string) ([]json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.getKeyed(ctx, "/api", "apikey", url.Values{"cmd": {command}}, &raw); err != nil {
		return nil, err
	}
	var list []json.RawMessage
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var wrapped struct {
		Success *bool           `json:"success"`
		Data    json.RawMessage `json:"data"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("arrimport: mylar %s: unreadable answer: %w", command, err)
	}
	if wrapped.Error != nil {
		return nil, fmt.Errorf("arrimport: mylar %s: %s", command, wrapped.Error.Message)
	}
	if err := json.Unmarshal(wrapped.Data, &list); err != nil {
		return nil, fmt.Errorf("arrimport: mylar %s: no list in the answer", command)
	}
	return list, nil
}

// SystemStatus is the connection test: Mylar3 has no version call that needs less than the index, so it reads that.
func (c MylarClient) SystemStatus(ctx context.Context) (LidarrStatus, error) {
	_, err := mylarList(ctx, c.inner, "getIndex")
	return LidarrStatus{}, err
}

// Comics answers every series Mylar3 watches. It has no root folders: a series is placed by the folder it lives in.
func (c MylarClient) Comics(ctx context.Context) ([]ComicSeries, error) {
	rows, err := mylarList(ctx, c.inner, "getIndex")
	if err != nil {
		return nil, err
	}
	series := make([]ComicSeries, 0, len(rows))
	for _, row := range rows {
		var comic struct {
			ID       string          `json:"ComicID"`
			Name     string          `json:"ComicName"`
			Year     string          `json:"ComicYear"`
			Status   string          `json:"Status"`
			Location string          `json:"ComicLocation"`
			Have     json.RawMessage `json:"Have"`
		}
		if err := json.Unmarshal(row, &comic); err != nil {
			return nil, fmt.Errorf("arrimport: mylar getIndex: %w", err)
		}
		year, _ := strconv.Atoi(strings.TrimSpace(comic.Year))
		series = append(series, ComicSeries{
			ID: comic.ID, Title: comic.Name, Year: year, ComicVine: strings.TrimPrefix(comic.ID, "4050-"),
			Monitored: !strings.EqualFold(comic.Status, "Paused") && !strings.EqualFold(comic.Status, "Ended"),
			HasFiles:  mylarHas(comic.Have),
			Folder:    comic.Location, RootFolder: path.Dir(comic.Location),
		})
	}
	return series, nil
}

// getKeyed is get for an app that takes its key as a query parameter, with the extra parameters given.
func (c LidarrClient) getKeyed(ctx context.Context, endpoint, keyParameter string, extra url.Values, into any) error {
	query := url.Values{keyParameter: {c.apiKey}}
	for name, values := range extra {
		query[name] = values
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return fmt.Errorf("arrimport: build request for %s: %w", endpoint, err)
	}
	response, err := c.client.Do(ctx, request)
	if err != nil {
		return fmt.Errorf("arrimport: %s: %w", endpoint, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxLidarrResponseBytes+1))
	if err != nil {
		return fmt.Errorf("arrimport: read response for %s: %w", endpoint, err)
	}
	if len(body) > maxLidarrResponseBytes {
		return fmt.Errorf("arrimport: %s: %w (over %d bytes)", endpoint, ErrResponseTooLarge, maxLidarrResponseBytes)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("arrimport: decode response for %s: %w", endpoint, err)
	}
	return nil
}

func comicVineText(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}

// mylarHas reads Mylar3's Have count, a number or a numeric string.
func mylarHas(have json.RawMessage) bool {
	switch strings.Trim(string(have), `" `) {
	case "", "0", "null":
		return false
	}
	return true
}
