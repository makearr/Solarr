package arrimport

import (
	"context"
	"strconv"
)

// WhisparrItem is GET /api/v3/movie of Whisparr v3 trimmed to what an import maps: a scene or a film, named by the
// StashDB id Whisparr keeps as its foreign id.
type WhisparrItem struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	ItemType         string `json:"itemType"`
	StashID          string `json:"stashId"`
	ForeignID        string `json:"foreignId"`
	TPDBID           string `json:"tpdbId"`
	StudioTitle      string `json:"studioTitle"`
	StudioForeignID  string `json:"studioForeignId"`
	ReleaseDate      string `json:"releaseDate"`
	Year             int    `json:"year"`
	Monitored        bool   `json:"monitored"`
	HasFile          bool   `json:"hasFile"`
	RootFolderPath   string `json:"rootFolderPath"`
	QualityProfileID int64  `json:"qualityProfileId"`
}

// Identifier is the id the scene is matched by: StashDB's, which Whisparr's foreign id is.
func (i WhisparrItem) Identifier() string {
	if i.StashID != "" {
		return i.StashID
	}
	return i.ForeignID
}

// WhisparrPerformer is GET /api/v3/performer trimmed to what an import maps.
type WhisparrPerformer struct {
	ID               int64  `json:"id"`
	FullName         string `json:"fullName"`
	ForeignID        string `json:"foreignId"`
	Monitored        bool   `json:"monitored"`
	RootFolderPath   string `json:"rootFolderPath"`
	QualityProfileID int64  `json:"qualityProfileId"`
	SceneCount       int    `json:"sceneCount"`
}

// WhisparrStudio is GET /api/v3/studio trimmed to what an import maps.
type WhisparrStudio struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	ForeignID        string `json:"foreignId"`
	TPDBID           string `json:"tpdbId"`
	Network          string `json:"network"`
	Monitored        bool   `json:"monitored"`
	RootFolderPath   string `json:"rootFolderPath"`
	QualityProfileID int64  `json:"qualityProfileId"`
}

// Whisparr paths of its resources, for the monitored flag a push writes.
const (
	WhisparrScenePath     = "/api/v3/movie"
	WhisparrPerformerPath = "/api/v3/performer"
	WhisparrStudioPath    = "/api/v3/studio"
)

// WhisparrClient is the REST client for one Whisparr v3 instance.
type WhisparrClient struct{ inner LidarrClient }

// Whisparr reads the same instance as a Whisparr: the transport and key are shared, only the API version differs.
func (c LidarrClient) Whisparr() WhisparrClient { return WhisparrClient{inner: c} }

// SystemStatus is the connection test.
func (c WhisparrClient) SystemStatus(ctx context.Context) (LidarrStatus, error) {
	var status LidarrStatus
	if err := c.inner.get(ctx, "/api/v3/system/status", &status); err != nil {
		return LidarrStatus{}, err
	}
	return status, nil
}

// RootFolders answers the instance's root folders.
func (c WhisparrClient) RootFolders(ctx context.Context) ([]LidarrRootFolder, error) {
	var folders []LidarrRootFolder
	if err := c.inner.get(ctx, "/api/v3/rootfolder", &folders); err != nil {
		return nil, err
	}
	return folders, nil
}

// Items answers every scene and film of the instance. Whisparr answers a large library as one response of over a hundred
// megabytes, minutes in coming, so it is decoded as it arrives.
func (c WhisparrClient) Items(ctx context.Context) ([]WhisparrItem, error) {
	var items []WhisparrItem
	if err := c.inner.getStream(ctx, WhisparrScenePath, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// Performers answers every performer the instance knows, monitored or not.
func (c WhisparrClient) Performers(ctx context.Context) ([]WhisparrPerformer, error) {
	var performers []WhisparrPerformer
	if err := c.inner.getStream(ctx, WhisparrPerformerPath, &performers); err != nil {
		return nil, err
	}
	return performers, nil
}

// Studios answers every studio the instance knows, monitored or not.
func (c WhisparrClient) Studios(ctx context.Context) ([]WhisparrStudio, error) {
	var studios []WhisparrStudio
	if err := c.inner.getStream(ctx, WhisparrStudioPath, &studios); err != nil {
		return nil, err
	}
	return studios, nil
}

// QualityProfiles answers the instance's quality profiles.
func (c WhisparrClient) QualityProfiles(ctx context.Context) ([]SonarrQualityProfile, error) {
	var profiles []SonarrQualityProfile
	if err := c.inner.get(ctx, "/api/v3/qualityprofile", &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// DownloadClients answers the instance's download clients.
func (c WhisparrClient) DownloadClients(ctx context.Context) ([]DownloadClient, error) {
	return c.inner.downloadClients(ctx, "/api/v3/downloadclient")
}

// MediaManagement answers the instance's Media Management settings.
func (c WhisparrClient) MediaManagement(ctx context.Context) (MediaManagement, error) {
	var settings MediaManagement
	if err := c.inner.get(ctx, "/api/v3/config/mediamanagement", &settings); err != nil {
		return MediaManagement{}, err
	}
	return settings, nil
}

// Indexers answers the instance's indexers.
func (c WhisparrClient) Indexers(ctx context.Context) ([]Indexer, error) {
	return c.inner.indexers(ctx, "/api/v3/indexer")
}

// SetMonitored writes the monitored flag of one scene, performer or studio, by its resource path and id.
func (c WhisparrClient) SetMonitored(ctx context.Context, path string, id int64, monitored bool) error {
	return c.inner.SetMonitored(ctx, path, id, monitored)
}

// ResourcePath is path/id.
func ResourcePath(path string, id int64) string { return path + "/" + strconv.FormatInt(id, 10) }
