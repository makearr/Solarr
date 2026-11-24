package arrimport

import (
	"context"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ReadarrAuthor is GET /api/v1/author trimmed to what an import maps: the foreign id is the identity.
type ReadarrAuthor struct {
	ID                int64  `json:"id"`
	AuthorName        string `json:"authorName"`
	ForeignAuthorID   string `json:"foreignAuthorId"`
	Monitored         bool   `json:"monitored"`
	RootFolderPath    string `json:"rootFolderPath"`
	QualityProfileID  int64  `json:"qualityProfileId"`
	MetadataProfileID int64  `json:"metadataProfileId"`
}

// ReadarrBook is GET /api/v1/book trimmed to what an import maps. The foreign ids are the ones the instance's own book
// source names: a work's, and the edition it picked.
type ReadarrBook struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	AuthorTitle      string `json:"authorTitle"`
	ForeignBookID    string `json:"foreignBookId"`
	ForeignEditionID string `json:"foreignEditionId"`
	Monitored        bool   `json:"monitored"`
	ReleaseDate      string `json:"releaseDate"`
	Author           struct {
		ID               int64  `json:"id"`
		AuthorName       string `json:"authorName"`
		ForeignAuthorID  string `json:"foreignAuthorId"`
		RootFolderPath   string `json:"rootFolderPath"`
		QualityProfileID int64  `json:"qualityProfileId"`
	} `json:"author"`
	Statistics struct {
		BookFileCount int64 `json:"bookFileCount"`
	} `json:"statistics"`
}

// Year is the release year, 0 when the instance gives no date.
func (b ReadarrBook) Year() int {
	if len(b.ReleaseDate) < 4 {
		return 0
	}
	year := 0
	for _, digit := range b.ReleaseDate[:4] {
		if digit < '0' || digit > '9' {
			return 0
		}
		year = year*10 + int(digit-'0')
	}
	return year
}

// ReadarrClient is the REST client for one Readarr v0.4 instance (API v1): API key only (ADR-52); reads, plus the few
// writes of a companion.
type ReadarrClient struct{ inner LidarrClient }

// Readarr reads the same instance as a Readarr: the transport and key are shared.
func (c LidarrClient) Readarr() ReadarrClient { return ReadarrClient{inner: c} }

// NewReadarrClient builds one over an already-configured httpx.Client.
func NewReadarrClient(client *httpx.Client, baseURL, apiKey string) ReadarrClient {
	return ReadarrClient{inner: NewLidarrClient(client, baseURL, apiKey)}
}

// SystemStatus is the connection test.
func (c ReadarrClient) SystemStatus(ctx context.Context) (LidarrStatus, error) {
	var status LidarrStatus
	if err := c.inner.get(ctx, "/api/v1/system/status", &status); err != nil {
		return LidarrStatus{}, err
	}
	return status, nil
}

// RootFolders answers the instance's root folders.
func (c ReadarrClient) RootFolders(ctx context.Context) ([]LidarrRootFolder, error) {
	var folders []LidarrRootFolder
	if err := c.inner.get(ctx, "/api/v1/rootfolder", &folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// Authors answers every author in the instance.
func (c ReadarrClient) Authors(ctx context.Context) ([]ReadarrAuthor, error) {
	var authors []ReadarrAuthor
	if err := c.inner.get(ctx, "/api/v1/author", &authors); err != nil {
		return nil, err
	}
	return authors, nil
}

// Books answers every book in the instance, each with its author.
func (c ReadarrClient) Books(ctx context.Context) ([]ReadarrBook, error) {
	var books []ReadarrBook
	if err := c.inner.get(ctx, "/api/v1/book", &books); err != nil {
		return nil, err
	}
	return books, nil
}

// QualityProfiles answers the instance's quality profiles.
func (c ReadarrClient) QualityProfiles(ctx context.Context) ([]SonarrQualityProfile, error) {
	var profiles []SonarrQualityProfile
	if err := c.inner.get(ctx, "/api/v1/qualityprofile", &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// MetadataProfiles answers the instance's metadata profiles.
func (c ReadarrClient) MetadataProfiles(ctx context.Context) ([]LidarrMetadataProfile, error) {
	var profiles []LidarrMetadataProfile
	if err := c.inner.get(ctx, "/api/v1/metadataprofile", &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// DownloadClients answers the instance's download clients, their address and category.
func (c ReadarrClient) DownloadClients(ctx context.Context) ([]DownloadClient, error) {
	return c.inner.downloadClients(ctx, "/api/v1/downloadclient")
}

// RemotePathMappings answers the instance's Remote Path Mappings.
func (c ReadarrClient) RemotePathMappings(ctx context.Context) ([]RemotePathMapping, error) {
	return c.inner.remotePathMappings(ctx, "/api/v1/remotepathmapping")
}

// MediaManagement answers the instance's Media Management settings.
func (c ReadarrClient) MediaManagement(ctx context.Context) (MediaManagement, error) {
	var settings MediaManagement
	if err := c.inner.get(ctx, "/api/v1/config/mediamanagement", &settings); err != nil {
		return MediaManagement{}, err
	}
	return settings, nil
}

// ImportLists answers the instance's import lists.
func (c ReadarrClient) ImportLists(ctx context.Context) ([]ImportList, error) {
	return c.inner.importLists(ctx, "/api/v1/importlist")
}

// Indexers answers the instance's indexers.
func (c ReadarrClient) Indexers(ctx context.Context) ([]Indexer, error) {
	return c.inner.indexers(ctx, "/api/v1/indexer")
}

// SetMonitored writes the monitored flag of one resource, by its collection path and id.
func (c ReadarrClient) SetMonitored(ctx context.Context, path string, id int64, monitored bool) error {
	return c.inner.SetMonitored(ctx, path, id, monitored)
}

// LookupBook finds a book by the id its source names (a Goodreads work id); the id is Readarr's when it holds the book.
func (c ReadarrClient) LookupBook(ctx context.Context, foreignID string) (map[string]any, int64, bool, error) {
	found, err := c.inner.lookup(ctx, "/api/v1/book/lookup", "work:"+foreignID)
	if err != nil || len(found) == 0 {
		return nil, 0, false, err
	}
	return found[0], idOf(found[0]), true, nil
}

// AddBook adds the looked-up book, and its author when Readarr lacks it, as chosen; it answers Readarr's id.
func (c ReadarrClient) AddBook(ctx context.Context, book map[string]any, defaults AddDefaults) (int64, error) {
	delete(book, "id")
	author, _ := book["author"].(map[string]any)
	if author == nil {
		author = map[string]any{}
	}
	delete(author, "id")
	author["qualityProfileId"], author["metadataProfileId"] = defaults.QualityProfileID, defaults.MetadataProfileID
	author["rootFolderPath"], author["monitored"], author["monitorNewItems"] = defaults.RootFolderPath, true, "none"
	book["author"], book["monitored"] = author, defaults.Monitored
	book["addOptions"] = map[string]any{"addType": "automatic", "searchForNewBook": defaults.Search}
	body, err := c.inner.post(ctx, "/api/v1/book", book)
	if err != nil {
		return 0, err
	}
	return created(body)
}

// SearchBook has Readarr search for a book it already holds.
func (c ReadarrClient) SearchBook(ctx context.Context, id int64) error {
	return c.inner.command(ctx, "/api/v1/command", map[string]any{"name": "BookSearch", "bookIds": []int64{id}})
}
