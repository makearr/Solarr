package adapters

import (
	"context"
	"errors"
	"strconv"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/media"
)

// The adult type's words, as the media registry knows it: this package never imports a media module.
const (
	adultMediaType       = "adult"
	adultProviderStashDB = "stashdb"
	adultRolePerformer   = "performer"
	adultKindPerformer   = "performer"
	adultKindStudio      = "studio"
)

// The three kinds of thing a Whisparr holds: a scene or film, a performer and a studio.
const (
	whisparrScene = iota
	whisparrPerformer
	whisparrStudio
)

// whisparrEntry is one item of a Whisparr walk, whichever of the three kinds it is.
type whisparrEntry struct {
	kind      int
	scene     arrimport.WhisparrItem
	performer arrimport.WhisparrPerformer
	studio    arrimport.WhisparrStudio
}

func (e whisparrEntry) describe() arrItem {
	switch e.kind {
	case whisparrScene:
		return arrItem{Name: e.scene.Title, RootFolder: e.scene.RootFolderPath, Monitored: e.scene.Monitored}
	case whisparrPerformer:
		return arrItem{Name: e.performer.FullName, RootFolder: e.performer.RootFolderPath, Monitored: e.performer.Monitored}
	}
	return arrItem{Name: e.studio.Title, RootFolder: e.studio.RootFolderPath, Monitored: e.studio.Monitored}
}

// whisparrSource walks Whisparr's performers and studios, then its scenes: a monitored performer or studio becomes a
// follow, a scene is added by its StashDB id. Everything is counted in the Artists columns, the run's first-kind tally. A
// monitoring-only run sets the flags of what is held and adds nothing.
func (a ArrSync) whisparrSource(importID int64, client arrimport.WhisparrClient, options apiserver.ArrSyncOptions) arrSource[whisparrEntry] {
	return arrSource[whisparrEntry]{
		connect: func(ctx context.Context) error {
			if a.Adult == nil {
				return errors.New("no adult service")
			}
			_, err := client.SystemStatus(ctx)
			return err
		},
		list: func(ctx context.Context) ([]whisparrEntry, error) {
			var entries []whisparrEntry
			performers, err := client.Performers(ctx)
			if err != nil {
				return nil, err
			}
			for _, performer := range performers {
				entries = append(entries, whisparrEntry{kind: whisparrPerformer, performer: performer})
			}
			studios, err := client.Studios(ctx)
			if err != nil {
				return nil, err
			}
			for _, studio := range studios {
				entries = append(entries, whisparrEntry{kind: whisparrStudio, studio: studio})
			}
			scenes, err := client.Items(ctx)
			if err != nil {
				return nil, err
			}
			for _, scene := range scenes {
				entries = append(entries, whisparrEntry{kind: whisparrScene, scene: scene})
			}
			return entries, nil
		},
		describe: whisparrEntry.describe,
		workers:  2,
		sync: func(ctx context.Context, categoryID int64, entry whisparrEntry) (library.ArrImportCounts, error) {
			created, err := a.syncWhisparr(ctx, importID, categoryID, entry, options.ProfileMap)
			switch {
			case err != nil:
				return library.ArrImportCounts{}, err
			case created:
				return library.ArrImportCounts{ArtistsCreated: 1}, nil
			}
			return library.ArrImportCounts{ArtistsMatched: 1}, nil
		},
		monitor: a.monitorWhisparr,
	}
}

// syncWhisparr adds one item through the adult type's own add, as the add dialog would: a held one is matched.
func (a ArrSync) syncWhisparr(ctx context.Context, importID, categoryID int64, entry whisparrEntry, profileMap map[int64]*int64) (bool, error) {
	var request media.AddRequest
	var title, sourceID string
	kind := library.ArrImportItemMovie
	switch entry.kind {
	case whisparrScene:
		scene := entry.scene
		if scene.Identifier() == "" {
			return false, errors.New("no StashDB id")
		}
		request = media.AddRequest{CategoryID: categoryID, Kind: adultMediaType, Provider: adultProviderStashDB, ExternalID: scene.Identifier(), Monitored: scene.Monitored}
		if profile, mapped := profileMap[scene.QualityProfileID]; mapped && profile != nil {
			request.ProfileID = profile
		}
		request.Fallback = &media.Fallback{Title: scene.Title, Year: scene.Year}
		title, sourceID = scene.Title, strconv.FormatInt(scene.ID, 10)
	case whisparrPerformer:
		performer := entry.performer
		if !performer.Monitored {
			return false, nil
		}
		request = media.AddRequest{CategoryID: categoryID, Kind: adultKindPerformer, Provider: "stashdb:performer", ExternalID: performer.ForeignID, Monitored: true}
		title, sourceID = performer.FullName, strconv.FormatInt(performer.ID, 10)
		kind = library.ArrImportItemArtist
	default:
		studio := entry.studio
		if !studio.Monitored {
			return false, nil
		}
		request = media.AddRequest{CategoryID: categoryID, Kind: adultKindStudio, Provider: "stashdb:studio", ExternalID: studio.ForeignID, Monitored: true}
		title, sourceID = studio.Title, strconv.FormatInt(studio.ID, 10)
		kind = library.ArrImportItemArtist
	}
	result, err := a.Adult.AddWork(ctx, request)
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
	if _, err := a.Imports.AddItem(ctx, importID, item, a.Clock.Now().Unix()); err != nil {
		return false, err
	}
	switch {
	case result.WorkID != 0:
		a.link(ctx, linkScene, entry.scene.ID, "work", result.WorkID)
	case result.PersonID != 0:
		a.link(ctx, linkPerformer, entry.performer.ID, "person", result.PersonID)
	case entry.kind == whisparrStudio:
		if organisationID, held, err := a.Organisations.FindByExternalID(ctx, "stashdb:studio", entry.studio.ForeignID); err == nil && held {
			a.link(ctx, linkStudio, entry.studio.ID, "organisation", organisationID)
		}
	}
	return result.Created, nil
}
