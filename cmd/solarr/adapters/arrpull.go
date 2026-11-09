package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
)

// pullOptions is what a connection's stored pull asks of one import run.
func pullOptions(instance settings.ArrInstance) apiserver.ArrSyncOptions {
	pull := instance.Pull()
	options := apiserver.ArrSyncOptions{
		Monitored: pull.Monitored, Unmonitored: pull.Unmonitored, Present: pull.Present, Albums: pull.Albums,
		Indexers: pull.Indexers, DownloadClients: pull.DownloadClients, RootFolders: pull.RootFolders, ImportLists: pull.ImportLists,
	}
	if options.Albums == "" {
		options.Albums = "all"
	}
	if pull.LibraryID != 0 {
		libraryID := pull.LibraryID
		options.NewFoldersLibrary = &libraryID
	}
	for _, rewrite := range pull.PathRewrites {
		options.PathRewrites = append(options.PathRewrites, apiserver.PathRewrite{From: rewrite.From, To: rewrite.To})
	}
	return options
}

// Pull runs one import by the options stored on the connection.
func (a ArrSync) Pull(ctx context.Context, instanceID int64) (apiserver.ArrImportRun, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportRun{}, err
	}
	return a.Sync(ctx, instanceID, pullOptions(instance))
}

// ArrPuller is the scheduled pull of every enabled content connection whose pull is automatic.
type ArrPuller struct {
	Sync   ArrSync
	Logger *slog.Logger
}

// Run implements jobs.Handler; one connection failing does not stop the others.
func (p ArrPuller) Run(ctx context.Context, _ jobs.Job) error {
	instances, err := p.Sync.Cache.ArrInstances(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, instance := range instances {
		if instance.App == settings.ArrAppProwlarr || !instance.Enabled || !instance.Pull().Automatic || !p.Sync.supported(instance.App) {
			continue
		}
		if _, err := p.Sync.Pull(ctx, instance.ID); err != nil && !errors.Is(err, apiserver.ErrArrSyncAlreadyRunning) {
			failures = append(failures, fmt.Errorf("%s: %w", instance.Name, err))
		}
	}
	return errors.Join(failures...)
}

// arrRootFolders reads the root folders of an *Arr by its app's API.
func arrRootFolders(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) ([]arrimport.LidarrRootFolder, error) {
	switch app {
	case settings.ArrAppSonarr:
		return client.Sonarr().RootFolders(ctx)
	case settings.ArrAppRadarr:
		return client.Radarr().RootFolders(ctx)
	case settings.ArrAppWhisparr:
		return client.Whisparr().RootFolders(ctx)
	default:
		return client.RootFolders(ctx)
	}
}

// pullLibrary is the library the pull's root folders and import lists go to: the one chosen, else the connection's.
func pullLibrary(instance settings.ArrInstance, options apiserver.ArrSyncOptions) (int64, bool) {
	if options.NewFoldersLibrary != nil {
		return *options.NewFoldersLibrary, true
	}
	if instance.CategoryID != nil {
		return *instance.CategoryID, true
	}
	return 0, false
}

// rootFolders adds the *Arr's root folders (read here by the path rewrites) to the library; one already known is only attached.
func (a ArrSync) rootFolders(ctx context.Context, instance settings.ArrInstance, client arrimport.LidarrClient, options apiserver.ArrSyncOptions) error {
	if !options.RootFolders || a.Setup.Cache == nil {
		return nil
	}
	libraryID, chosen := pullLibrary(instance, options)
	if !chosen {
		return errors.New("root folders: no library to add them to")
	}
	folders, err := arrRootFolders(ctx, instance.App, client)
	if err != nil {
		return fmt.Errorf("root folders: %w", err)
	}
	var failures []error
	for _, folder := range folders {
		path := rewritePath(folder.Path, options.PathRewrites)
		info, statErr := a.Setup.FS.Stat(path)
		if statErr != nil || !info.IsDir {
			failures = append(failures, fmt.Errorf("root folder %s: not a folder here (add a path rewrite)", path))
			continue
		}
		rootID := int64(0)
		for _, existing := range a.Cache.Snapshot().RootFolders() {
			if existing.Path == path {
				rootID = existing.ID
			}
		}
		if rootID == 0 {
			created, err := a.Cache.CreateRootFolder(ctx, path, "")
			if err != nil {
				failures = append(failures, fmt.Errorf("root folder %s: %w", path, err))
				continue
			}
			rootID = created.ID
		}
		if err := a.Cache.AttachRootFolder(ctx, settings.CategoryRoot{CategoryID: libraryID, RootFolderID: rootID, Weight: 1, AcceptsNew: true}); err != nil {
			failures = append(failures, fmt.Errorf("root folder %s: %w", path, err))
		}
	}
	if a.AfterFolders != nil {
		a.AfterFolders(ctx)
	}
	return errors.Join(failures...)
}

// importLists adds the *Arr's TMDb and MDBList lists to the library, each switched off to be previewed first; a list of
// any other kind has nothing Solarr reads and is named in the run's error.
func (a ArrSync) importLists(ctx context.Context, instance settings.ArrInstance, client arrimport.LidarrClient, options apiserver.ArrSyncOptions) error {
	if !options.ImportLists || a.Setup.Cache == nil {
		return nil
	}
	libraryID, chosen := pullLibrary(instance, options)
	if !chosen {
		return errors.New("import lists: no library to add them to")
	}
	category, found := a.Cache.Snapshot().Category(libraryID)
	if !found {
		return fmt.Errorf("import lists: library %d not found", libraryID)
	}
	var found3 []arrimport.ImportList
	var err error
	switch instance.App {
	case settings.ArrAppSonarr:
		found3, err = client.Sonarr().ImportLists(ctx)
	case settings.ArrAppRadarr:
		found3, err = client.Radarr().ImportLists(ctx)
	case settings.ArrAppWhisparr:
		found3, err = client.Whisparr().ImportLists(ctx)
	default:
		found3, err = client.ImportLists(ctx)
	}
	if err != nil {
		return fmt.Errorf("import lists: %w", err)
	}
	have, err := a.Cache.ImportLists(ctx, libraryID)
	if err != nil {
		return fmt.Errorf("import lists: %w", err)
	}
	known := map[string]bool{}
	for _, existing := range have {
		known[existing.Type+"|"+existing.Options] = true
	}
	var failures, skipped []string
	for _, entry := range found3 {
		listType, optionKey, optionValue, mapped := mapArrImportList(entry)
		if !mapped {
			skipped = append(skipped, entry.Name+" ("+entry.Implementation+")")
			continue
		}
		encoded, _ := json.Marshal(map[string]string{optionKey: optionValue})
		if known[string(listType)+"|"+string(encoded)] {
			continue
		}
		request := settings.ImportListRequest{
			Name: entry.Name, Type: string(listType), CadenceHours: 24, Monitor: true, Enabled: false, Options: string(encoded),
		}
		if _, err := a.Cache.CreateImportList(ctx, libraryID, request); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", entry.Name, err))
		}
	}
	if len(skipped) > 0 {
		failures = append(failures, "no equivalent here for "+strings.Join(skipped, ", "))
	}
	if len(failures) > 0 {
		return fmt.Errorf("import lists into %s: %s", category.Name, strings.Join(failures, "; "))
	}
	return nil
}

// mapArrImportList reads an *Arr list as the Solarr list type that reads the same list, with its one setting.
func mapArrImportList(entry arrimport.ImportList) (media.ListType, string, string, bool) {
	switch strings.ToLower(entry.Implementation) {
	case "tmdblistimport", "tmdblist":
		if id := entry.Fields["listId"]; id != "" {
			return media.ListTMDBList, "list", id, true
		}
	case "mdblistimport", "mdblist":
		for _, name := range []string{"listId", "linkUrl", "url"} {
			if value := entry.Fields[name]; value != "" {
				return media.ListMDBList, "mdblist", value, true
			}
		}
	}
	return "", "", "", false
}
