package adapters

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// previewItem is one *Arr item as a preview counts it: its root folder, profile, and the id it would be matched by.
type previewItem struct {
	rootFolder string
	monitored  bool
	profileID  int64
	// provider and externalID find it in Solarr; an empty externalID is an item the import skips.
	provider, externalID string
	// person says the id is a person's (a Lidarr artist), not a work's.
	person bool
}

// previewSource is what one app gives a preview: its items, root folders, profiles and Media Management.
type previewSource struct {
	itemKind  string
	mediaType string
	items     []previewItem
	roots     []string
	profiles  map[int64]string
	settings  arrimport.MediaManagement
}

// Preview reads what Sync would do with options and changes nothing (ADR-52's preview): per root folder where it is
// here and what of it is new, per profile the Solarr profile proposed, per library the settings that would change.
func (a ArrSync) Preview(ctx context.Context, instanceID int64, options apiserver.ArrSyncOptions) (apiserver.ArrImportPreview, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportPreview{}, err
	}
	if !arrAppSupported(instance.App) {
		return apiserver.ArrImportPreview{}, fmt.Errorf("%w: %s", apiserver.ErrArrAppNotYetSupported, instance.App)
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportPreview{}, err
	}
	source, err := readPreviewSource(ctx, instance.App, a.Lidarr(instance.BaseURL, apiKey.Reveal()))
	if err != nil {
		return apiserver.ArrImportPreview{}, err
	}

	snapshot := a.Cache.Snapshot()
	roots := snapshot.RootFolders()
	preview := apiserver.ArrImportPreview{ItemKind: source.itemKind}
	byPath := map[string]int{}
	rootFor := func(path string) *apiserver.ArrPreviewRoot {
		if index, seen := byPath[path]; seen {
			return &preview.Roots[index]
		}
		local := rewritePath(path, options.PathRewrites)
		root := apiserver.ArrPreviewRoot{Path: path, LocalPath: local}
		if folder, found := longestRoot(roots, local); found {
			id := folder.ID
			root.RootFolderID = &id
		}
		if categoryID, placed := a.categoryFor(instance, local, roots); placed {
			root.LibraryID = &categoryID
		} else if options.NewFoldersLibrary != nil {
			root.LibraryID = options.NewFoldersLibrary
		}
		byPath[path] = len(preview.Roots)
		preview.Roots = append(preview.Roots, root)
		return &preview.Roots[len(preview.Roots)-1]
	}
	for _, path := range source.roots {
		rootFor(path)
	}

	profileItems := map[int64]int64{}
	for _, item := range source.items {
		if (item.monitored && !options.Monitored) || (!item.monitored && !options.Unmonitored) {
			continue
		}
		root := rootFor(item.rootFolder)
		root.Chosen++
		profileItems[item.profileID]++
		switch held, err := a.held(ctx, item); {
		case err != nil:
			return apiserver.ArrImportPreview{}, err
		case item.externalID == "":
			root.WithoutID++
		case held:
			root.InLibrary++
		default:
			root.New++
		}
	}

	for id, count := range profileItems {
		profile := apiserver.ArrPreviewProfile{ArrProfileID: id, Name: source.profiles[id], Items: count}
		if profile.Name == "" {
			profile.Name = "Profile " + strconv.FormatInt(id, 10)
		}
		profile.ProposedProfileID = sameNameProfile(snapshot.QualityProfiles(), source.mediaType, source.profiles[id])
		preview.Profiles = append(preview.Profiles, profile)
	}
	slices.SortFunc(preview.Profiles, func(x, y apiserver.ArrPreviewProfile) int { return int(y.Items - x.Items) })

	var libraries []int64
	for _, root := range preview.Roots {
		if root.LibraryID != nil && root.Chosen > 0 && !slices.Contains(libraries, *root.LibraryID) {
			libraries = append(libraries, *root.LibraryID)
		}
	}
	for _, id := range libraries {
		category, found := snapshot.Category(id)
		if !found {
			continue
		}
		_, changes := arrFileManagement(category.FileManagement, source.settings, instance.App, a.extraFileDefaults(category.MediaType))
		preview.Settings = append(preview.Settings, apiserver.ArrPreviewSettings{LibraryID: id, Changes: changes})
	}
	return preview, nil
}

// readPreviewSource reads one app's items, root folders, profiles and Media Management, live.
func readPreviewSource(ctx context.Context, app settings.ArrApp, client arrimport.LidarrClient) (previewSource, error) {
	var (
		source   previewSource
		folders  []arrimport.LidarrRootFolder
		profiles []arrimport.SonarrQualityProfile
		err      error
	)
	switch app {
	case settings.ArrAppSonarr:
		sonarr := client.Sonarr()
		source.itemKind, source.mediaType = "series", "tv"
		series, listErr := sonarr.Series(ctx)
		if listErr != nil {
			return previewSource{}, listErr
		}
		for _, one := range series {
			source.items = append(source.items, previewItem{rootFolder: one.RootFolderPath, monitored: one.Monitored,
				profileID: one.QualityProfileID, provider: tvProviderTVDB, externalID: nonZero(one.TVDBID)})
		}
		folders, _ = sonarr.RootFolders(ctx)
		profiles, _ = sonarr.QualityProfiles(ctx)
		source.settings, err = sonarr.MediaManagement(ctx)
	case settings.ArrAppRadarr:
		radarr := client.Radarr()
		source.itemKind, source.mediaType = "movies", "movie"
		movies, listErr := radarr.Movies(ctx)
		if listErr != nil {
			return previewSource{}, listErr
		}
		for _, movie := range movies {
			source.items = append(source.items, previewItem{rootFolder: movie.RootFolderPath, monitored: movie.Monitored,
				profileID: movie.QualityProfileID, provider: movieProviderTMDB, externalID: nonZero(movie.TMDBID)})
		}
		folders, _ = radarr.RootFolders(ctx)
		profiles, _ = radarr.QualityProfiles(ctx)
		source.settings, err = radarr.MediaManagement(ctx)
	case settings.ArrAppWhisparr:
		whisparr := client.Whisparr()
		source.itemKind, source.mediaType = "scenes", "adult"
		scenes, listErr := whisparr.Items(ctx)
		if listErr != nil {
			return previewSource{}, listErr
		}
		for _, scene := range scenes {
			source.items = append(source.items, previewItem{rootFolder: scene.RootFolderPath, monitored: scene.Monitored,
				profileID: scene.QualityProfileID, provider: adultProviderStashDB, externalID: scene.Identifier()})
		}
		folders, _ = whisparr.RootFolders(ctx)
		profiles, _ = whisparr.QualityProfiles(ctx)
		source.settings, err = whisparr.MediaManagement(ctx)
	default:
		source.itemKind, source.mediaType = "artists", "music"
		artists, listErr := client.Artists(ctx)
		if listErr != nil {
			return previewSource{}, listErr
		}
		for _, artist := range artists {
			source.items = append(source.items, previewItem{rootFolder: artist.RootFolderPath, monitored: artist.Monitored,
				profileID: artist.QualityProfileID, provider: musicProviderArtist, externalID: artist.ForeignArtistID, person: true})
		}
		folders, _ = client.RootFolders(ctx)
		lidarrProfiles, _ := client.QualityProfiles(ctx)
		for _, profile := range lidarrProfiles {
			profiles = append(profiles, arrimport.SonarrQualityProfile(profile))
		}
		source.settings, err = client.MediaManagement(ctx)
	}
	if err != nil {
		return previewSource{}, fmt.Errorf("media management: %w", err)
	}
	for _, folder := range folders {
		source.roots = append(source.roots, folder.Path)
	}
	source.profiles = make(map[int64]string, len(profiles))
	for _, profile := range profiles {
		source.profiles[profile.ID] = profile.Name
	}
	return source, nil
}

// held reports an item Solarr already holds by its id; the import matches it and leaves it as it is.
func (a ArrSync) held(ctx context.Context, item previewItem) (bool, error) {
	if item.externalID == "" {
		return false, nil
	}
	if item.person {
		_, found, err := a.People.FindByExternalID(ctx, item.provider, item.externalID)
		return found, err
	}
	_, found, err := a.Identity.WorkByExternalID(ctx, item.provider, item.externalID)
	return found, err
}

// extraFileDefaults is what a media type imports along with its files when a library has not said.
func (a ArrSync) extraFileDefaults(mediaType string) media.ExtraFilesDefault {
	if a.Registry == nil {
		return media.ExtraFilesDefault{}
	}
	module, found := a.Registry.Module(media.Key(mediaType))
	if !found {
		return media.ExtraFilesDefault{}
	}
	return module.Descriptor().ExtraFiles
}

// longestRoot is the root folder here a path falls under, the longest when roots nest.
func longestRoot(roots []settings.RootFolder, path string) (settings.RootFolder, bool) {
	var best settings.RootFolder
	found := false
	for _, root := range roots {
		if fsx.Within(root.Path, path) && (!found || len(root.Path) > len(best.Path)) {
			best, found = root, true
		}
	}
	return best, found
}

// sameNameProfile is the Solarr profile of a media type named like an *Arr profile (a shipped movie profile's name
// leads with "Movie "), nil for none: the rule an import without a mapping follows.
func sameNameProfile(profiles []settings.QualityProfile, mediaType, name string) *int64 {
	if name == "" {
		return nil
	}
	for _, profile := range profiles {
		if profile.MediaType == mediaType && strings.EqualFold(strings.TrimPrefix(profile.Name, "Movie "), name) {
			id := profile.ID
			return &id
		}
	}
	return nil
}

func nonZero(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
