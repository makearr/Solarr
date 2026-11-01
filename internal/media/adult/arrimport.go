package adult

import (
	"context"
	"errors"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/media"
)

// The three kinds of thing a Whisparr holds: a scene or film, a performer and a studio.
const (
	entryScene = iota
	entryPerformer
	entryStudio
)

// whisparrEntry is one item of a Whisparr walk, whichever of the three kinds it is.
type whisparrEntry struct {
	kind      int
	scene     arrimport.WhisparrItem
	performer arrimport.WhisparrPerformer
	studio    arrimport.WhisparrStudio
}

func (e whisparrEntry) describe() arrmap.Item {
	switch e.kind {
	case entryScene:
		return arrmap.Item{Name: e.scene.Title, RootFolder: e.scene.RootFolderPath, Monitored: e.scene.Monitored}
	case entryPerformer:
		return arrmap.Item{Name: e.performer.FullName, RootFolder: e.performer.RootFolderPath, Monitored: e.performer.Monitored}
	}
	return arrmap.Item{Name: e.studio.Title, RootFolder: e.studio.RootFolderPath, Monitored: e.studio.Monitored}
}

// WhisparrImport walks Whisparr's performers and studios, then its scenes: a monitored performer or studio becomes a
// follow, a scene is added by its StashDB id. Everything counts in the run's first-kind tally. A monitoring-only run sets
// the flags of what is held and adds nothing.
type WhisparrImport struct {
	Adding        media.Adding
	Organisations library.Organisations
	Monitors      arrmap.Monitors
}

// Import walks one Whisparr.
func (w WhisparrImport) Import(ctx context.Context, run arrmap.Run) (library.ArrImportCounts, error) {
	client := run.Client.Whisparr()
	return arrmap.Walk(ctx, run, arrmap.Source[whisparrEntry]{
		Connect: func(ctx context.Context) error {
			if w.Adding == nil {
				return errors.New("no adult service")
			}
			_, err := client.SystemStatus(ctx)
			return err
		},
		List: func(ctx context.Context) ([]whisparrEntry, error) {
			var entries []whisparrEntry
			performers, err := client.Performers(ctx)
			if err != nil {
				return nil, err
			}
			for _, performer := range performers {
				entries = append(entries, whisparrEntry{kind: entryPerformer, performer: performer})
			}
			studios, err := client.Studios(ctx)
			if err != nil {
				return nil, err
			}
			for _, studio := range studios {
				entries = append(entries, whisparrEntry{kind: entryStudio, studio: studio})
			}
			scenes, err := client.Items(ctx)
			if err != nil {
				return nil, err
			}
			for _, scene := range scenes {
				entries = append(entries, whisparrEntry{kind: entryScene, scene: scene})
			}
			return entries, nil
		},
		Describe: whisparrEntry.describe,
		Workers:  2,
		Sync: func(ctx context.Context, categoryID int64, entry whisparrEntry) (library.ArrImportCounts, error) {
			return arrmap.Tally(w.syncEntry(ctx, run, categoryID, entry))
		},
		Monitor: func(ctx context.Context, categoryID int64, entry whisparrEntry) (library.ArrImportCounts, error) {
			return w.monitorEntry(ctx, run, categoryID, entry)
		},
	})
}

// syncEntry adds one item through the type's own add, as the add dialog would: a held one is matched.
func (w WhisparrImport) syncEntry(ctx context.Context, run arrmap.Run, categoryID int64, entry whisparrEntry) (bool, error) {
	var request media.AddRequest
	var title, sourceID string
	kind := library.ArrImportItemMovie
	switch entry.kind {
	case entryScene:
		scene := entry.scene
		if scene.Identifier() == "" {
			return false, errors.New("no StashDB id")
		}
		request = media.AddRequest{CategoryID: categoryID, Kind: MediaType, Provider: ProviderStashDB, ExternalID: scene.Identifier(), Monitored: scene.Monitored}
		if profile, mapped := run.Options.ProfileMap[scene.QualityProfileID]; mapped && profile != nil {
			request.ProfileID = profile
		}
		request.Fallback = &media.Fallback{Title: scene.Title, Year: scene.Year}
		title, sourceID = scene.Title, strconv.FormatInt(scene.ID, 10)
	case entryPerformer:
		performer := entry.performer
		if !performer.Monitored {
			return false, nil
		}
		request = media.AddRequest{CategoryID: categoryID, Kind: KindPerformer, Provider: whisparrPerformerProvider, ExternalID: performer.ForeignID, Monitored: true}
		title, sourceID = performer.FullName, strconv.FormatInt(performer.ID, 10)
		kind = library.ArrImportItemArtist
	default:
		studio := entry.studio
		if !studio.Monitored {
			return false, nil
		}
		request = media.AddRequest{CategoryID: categoryID, Kind: KindStudio, Provider: whisparrStudioProvider, ExternalID: studio.ForeignID, Monitored: true}
		title, sourceID = studio.Title, strconv.FormatInt(studio.ID, 10)
		kind = library.ArrImportItemArtist
	}
	result, err := w.Adding.AddWork(ctx, request)
	if err != nil {
		return false, err
	}
	item := library.NewArrImportItem{Kind: kind, SourceID: sourceID, Title: title}
	switch {
	case result.WorkID != 0:
		workID := result.WorkID
		item.WorkID, item.WorkCreated = &workID, result.Created
	case result.PersonID != 0:
		personID := result.PersonID
		item.PersonID, item.PersonCreated = &personID, result.Created
	}
	if err := run.Record(ctx, item); err != nil {
		return false, err
	}
	switch {
	case result.WorkID != 0:
		run.Link(ctx, arrmap.LinkScene, entry.scene.ID, "work", result.WorkID)
	case result.PersonID != 0:
		run.Link(ctx, arrmap.LinkPerformer, entry.performer.ID, "person", result.PersonID)
	case entry.kind == entryStudio:
		if organisationID, held, err := w.Organisations.FindByExternalID(ctx, whisparrStudioProvider, entry.studio.ForeignID); err == nil && held {
			run.Link(ctx, arrmap.LinkStudio, entry.studio.ID, "organisation", organisationID)
		}
	}
	return result.Created, nil
}

// monitorEntry sets one of Whisparr's three kinds of item on what Solarr holds under the StashDB id Whisparr keeps for it.
func (w WhisparrImport) monitorEntry(ctx context.Context, run arrmap.Run, categoryID int64, entry whisparrEntry) (library.ArrImportCounts, error) {
	switch entry.kind {
	case entryScene:
		workID, held, err := w.Monitors.Work(ctx, MediaType, ProviderStashDB, entry.scene.Identifier(), entry.scene.Monitored)
		if held && err == nil {
			run.Link(ctx, arrmap.LinkScene, entry.scene.ID, "work", workID)
		}
		return arrmap.Found(held, err)
	case entryPerformer:
		personID, held, err := w.Monitors.Person(ctx, whisparrPerformerProvider, entry.performer.ForeignID, MediaType, RolePerformer, categoryID, entry.performer.Monitored)
		if held && err == nil {
			run.Link(ctx, arrmap.LinkPerformer, entry.performer.ID, "person", personID)
		}
		return arrmap.Found(held, err)
	}
	organisationID, held, err := w.Monitors.Organisation(ctx, whisparrStudioProvider, entry.studio.ForeignID, MediaType, categoryID, entry.studio.Monitored)
	if held && err == nil {
		run.Link(ctx, arrmap.LinkStudio, entry.studio.ID, "organisation", organisationID)
	}
	return arrmap.Found(held, err)
}

// Preview reads Whisparr's scenes.
func (w WhisparrImport) Preview(ctx context.Context, client arrimport.LidarrClient) (arrmap.PreviewSource, error) {
	whisparr := client.Whisparr()
	source := arrmap.PreviewSource{ItemKind: "scenes", MediaType: MediaType}
	scenes, err := whisparr.Items(ctx)
	if err != nil {
		return arrmap.PreviewSource{}, err
	}
	for _, scene := range scenes {
		source.Items = append(source.Items, arrmap.PreviewItem{RootFolder: scene.RootFolderPath, Monitored: scene.Monitored,
			ProfileID: scene.QualityProfileID, Provider: ProviderStashDB, ExternalID: scene.Identifier()})
	}
	names := arrmap.ProfileNames(ctx, whisparr.QualityProfiles, func(profile arrimport.SonarrQualityProfile) (int64, string) {
		return profile.ID, profile.Name
	})
	return arrmap.ReadPreview(ctx, source, whisparr, names)
}

// The ids Whisparr keeps for a performer and a studio are StashDB's.
const (
	whisparrPerformerProvider = "stashdb:performer"
	whisparrStudioProvider    = "stashdb:studio"
)
