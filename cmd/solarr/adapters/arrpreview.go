package adapters

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/fsx"
)

// Preview reads what Sync would do with options and changes nothing (ADR-52's preview): per root folder where it is
// here and what of it is new, per profile the Solarr profile proposed, per library the settings that would change.
func (a ArrSync) Preview(ctx context.Context, instanceID int64, options apiserver.ArrSyncOptions) (apiserver.ArrImportPreview, error) {
	instance, err := a.Cache.GetArrInstance(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportPreview{}, err
	}
	if !a.supported(instance.App) {
		return apiserver.ArrImportPreview{}, fmt.Errorf("%w: %s", apiserver.ErrArrAppNotYetSupported, instance.App)
	}
	apiKey, err := a.Cache.DecryptedAPIKey(ctx, instanceID)
	if err != nil {
		return apiserver.ArrImportPreview{}, err
	}
	source, err := a.Importers[instance.App].Preview(ctx, a.Lidarr(instance.BaseURL, apiKey.Reveal()))
	if err != nil {
		return apiserver.ArrImportPreview{}, err
	}

	snapshot := a.Cache.Snapshot()
	roots := snapshot.RootFolders()
	preview := apiserver.ArrImportPreview{ItemKind: source.ItemKind}
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
	for _, path := range source.Roots {
		rootFor(path)
	}

	profileItems := map[int64]int64{}
	for _, item := range source.Items {
		if (item.Monitored && !options.Monitored) || (!item.Monitored && !options.Unmonitored) {
			continue
		}
		root := rootFor(item.RootFolder)
		root.Chosen++
		profileItems[item.ProfileID]++
		switch held, err := a.held(ctx, item); {
		case err != nil:
			return apiserver.ArrImportPreview{}, err
		case item.ExternalID == "":
			root.WithoutID++
		case held:
			root.InLibrary++
		default:
			root.New++
		}
	}

	for id, count := range profileItems {
		profile := apiserver.ArrPreviewProfile{ArrProfileID: id, Name: source.Profiles[id], Items: count}
		if profile.Name == "" {
			profile.Name = "Profile " + strconv.FormatInt(id, 10)
		}
		profile.ProposedProfileID = arrmap.SameNameProfile(snapshot.QualityProfiles(), source.MediaType, source.Profiles[id])
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
		_, changes := arrFileManagement(category.FileManagement, source.Settings, instance.App, a.extraFileDefaults(category.MediaType))
		preview.Settings = append(preview.Settings, apiserver.ArrPreviewSettings{LibraryID: id, Changes: changes})
	}
	return preview, nil
}

// held reports an item Solarr already holds by its id; the import matches it and leaves it as it is.
func (a ArrSync) held(ctx context.Context, item arrmap.PreviewItem) (bool, error) {
	if item.ExternalID == "" {
		return false, nil
	}
	if item.Person {
		_, found, err := a.People.FindByExternalID(ctx, item.Provider, item.ExternalID)
		return found, err
	}
	_, found, err := a.Identity.WorkByExternalID(ctx, item.Provider, item.ExternalID)
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
