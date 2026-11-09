package music

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/importing/arrimport"
	"github.com/makearr/Solarr/internal/importing/arrmap"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// LidarrImport adopts Lidarr's artists and albums from Lidarr's own record: a monitored artist becomes person and
// subscription, an album a work with its wanted item, each matched by its MusicBrainz id (ADR-8) so a Lidarr-driven work
// and a scan-driven one land on the same row. It asks no metadata source: 16k albums at MusicBrainz's 1/s would take hours.
type LidarrImport struct {
	Categories       Categories
	Identity         library.Identity
	People           library.People
	Desired          library.Desired
	Monitors         arrmap.Monitors
	DefaultProfileID int64
	Clock            clock.Clock
}

// Import walks Lidarr's artists; one album failing does not stop the artist's others.
func (l LidarrImport) Import(ctx context.Context, run arrmap.Run) (library.ArrImportCounts, error) {
	client, options := run.Client, run.Options
	return arrmap.Walk(ctx, run, arrmap.Source[arrimport.LidarrArtist]{
		Connect: func(ctx context.Context) error { _, err := client.SystemStatus(ctx); return err },
		List:    client.Artists,
		Describe: func(artist arrimport.LidarrArtist) arrmap.Item {
			return arrmap.Item{Name: artist.ArtistName, RootFolder: artist.RootFolderPath, Monitored: artist.Monitored, HasFiles: artist.HasFiles()}
		},
		Workers: 1,
		Monitor: func(ctx context.Context, _ int64, artist arrimport.LidarrArtist) (library.ArrImportCounts, error) {
			return l.monitorArtist(ctx, run, artist)
		},
		Sync: func(ctx context.Context, categoryID int64, artist arrimport.LidarrArtist) (library.ArrImportCounts, error) {
			var counts library.ArrImportCounts
			personID, created, err := l.adoptArtist(ctx, run, artist)
			if err != nil {
				return counts, err
			}
			if created {
				counts.ArtistsCreated++
			} else {
				counts.ArtistsMatched++
			}
			albums, err := client.Albums(ctx, artist.ID)
			if err != nil {
				return counts, fmt.Errorf("list albums: %w", err)
			}
			var failures []string
			for _, album := range albums {
				wanted := (album.Monitored && options.Monitored) || (!album.Monitored && options.Unmonitored) || (options.Present && album.Statistics.TrackFileCount > 0)
				if !wanted || !albumWanted(album, options.Albums) {
					continue
				}
				workCreated, err := l.adoptAlbum(ctx, run, categoryID, personID, album, options.ProfileMap[artist.QualityProfileID])
				switch {
				case err != nil:
					failures = append(failures, fmt.Sprintf("%s: %v", album.Title, err))
				case workCreated:
					counts.AlbumsCreated++
				default:
					counts.AlbumsMatched++
				}
			}
			if len(failures) > 0 {
				return counts, errors.New(strings.Join(failures, "; "))
			}
			return counts, nil
		},
	})
}

// adoptArtist resolves-or-creates the person behind the artist's MusicBrainz id and ensures their subscription,
// recording one item either way. A brand-new person cannot have had a subscription, so one is created exactly with them.
func (l LidarrImport) adoptArtist(ctx context.Context, run arrmap.Run, artist arrimport.LidarrArtist) (personID int64, created bool, err error) {
	if artist.ForeignArtistID == "" {
		return 0, false, errors.New("no MusicBrainz artist id")
	}
	now := l.Clock.Now()

	_, found, err := l.People.FindByExternalID(ctx, ProviderArtist, artist.ForeignArtistID)
	if err != nil {
		return 0, false, err
	}
	personID, err = l.People.ResolvePerson(ctx, now, ArtistLink(artist.ForeignArtistID, library.PersonIdentity{Name: artist.ArtistName}))
	if err != nil {
		return 0, false, err
	}
	created = !found

	// An unmonitored artist arrives as an artist in the library, not one followed for new albums.
	var subscription *int64
	if artist.Monitored {
		subscriptionID, err := l.People.Subscribe(ctx, now, personID, 0, library.SubscriptionRequest{
			MediaTypes: []string{string(MediaType)}, Roles: []string{CreditRoleArtist},
		})
		if err != nil {
			return 0, false, err
		}
		subscription = &subscriptionID
	}

	if err := run.Record(ctx, library.NewArrImportItem{
		Kind: library.ArrImportItemArtist, SourceID: strconv.FormatInt(artist.ID, 10), Title: artist.ArtistName,
		PersonID: &personID, PersonCreated: created, SubscriptionID: subscription,
	}); err != nil {
		return 0, false, err
	}
	run.Link(ctx, arrmap.LinkArtist, artist.ID, "person", personID)
	return personID, created, nil
}

// adoptAlbum resolves-or-creates the work behind the album's release-group id and ensures its album-level item, recording
// one item either way. A new work gets personID as primary artist; a matched one keeps the credits it has.
func (l LidarrImport) adoptAlbum(
	ctx context.Context, run arrmap.Run, categoryID, personID int64, album arrimport.LidarrAlbum, mapped *int64,
) (workCreated bool, err error) {
	if album.ForeignAlbumID == "" {
		return false, errors.New("no MusicBrainz release-group id")
	}
	resolutions, err := l.Identity.ResolveWorksDetailed(ctx, l.Clock.Now(), []library.WorkLink{AlbumLink(album.ForeignAlbumID, album.Title)})
	if err != nil {
		return false, err
	}
	resolution := resolutions[0]

	if resolution.WorkCreated {
		if err := l.People.AddLeadCredit(ctx, resolution.WorkID, personID, CreditRoleArtist); err != nil {
			return false, err
		}
	}

	profileID := l.DefaultProfileID
	if category, ok := l.Categories.Category(categoryID); ok && category.QualityProfileID != nil {
		profileID = *category.QualityProfileID
	}
	if mapped != nil {
		profileID = *mapped
	}
	item, err := l.Desired.EnsureAlbumItem(ctx, library.AlbumRequest{
		WorkID: resolution.WorkID, CategoryID: categoryID, ProfileID: profileID, Monitored: album.Monitored,
	})
	if err != nil {
		return false, err
	}

	workID, itemID := resolution.WorkID, item.ID
	if err := run.Record(ctx, library.NewArrImportItem{
		Kind: library.ArrImportItemAlbum, SourceID: strconv.FormatInt(album.ID, 10), Title: album.Title,
		WorkID: &workID, WorkCreated: resolution.WorkCreated,
		// Informational only: revert acts on WorkCreated, which cascades the item with it.
		DesiredItemID: &itemID, DesiredItemCreated: resolution.WorkCreated,
	}); err != nil {
		return false, err
	}
	run.Link(ctx, arrmap.LinkAlbum, album.ID, "work", workID)
	return resolution.WorkCreated, nil
}

// monitorArtist gives the artist and the albums Solarr holds under their MusicBrainz ids the monitoring Lidarr holds.
func (l LidarrImport) monitorArtist(ctx context.Context, run arrmap.Run, artist arrimport.LidarrArtist) (library.ArrImportCounts, error) {
	personID, held, err := l.Monitors.Person(ctx, ProviderArtist, artist.ForeignArtistID, string(MediaType), CreditRoleArtist, 0, artist.Monitored)
	if err != nil || !held {
		return arrmap.Found(held, err)
	}
	run.Link(ctx, arrmap.LinkArtist, artist.ID, "person", personID)
	counts := library.ArrImportCounts{ArtistsMatched: 1}
	albums, err := run.Client.Albums(ctx, artist.ID)
	if err != nil {
		return counts, fmt.Errorf("list albums: %w", err)
	}
	for _, album := range albums {
		workID, albumHeld, err := l.Monitors.Work(ctx, string(MediaType), ProviderReleaseGroup, album.ForeignAlbumID, album.Monitored)
		if err != nil {
			return counts, fmt.Errorf("%s: %w", album.Title, err)
		}
		if albumHeld {
			counts.AlbumsMatched++
			run.Link(ctx, arrmap.LinkAlbum, album.ID, "work", workID)
		}
	}
	return counts, nil
}

// Preview reads Lidarr's artists; an album is not previewed on its own.
func (l LidarrImport) Preview(ctx context.Context, client arrimport.LidarrClient) (arrmap.PreviewSource, error) {
	source := arrmap.PreviewSource{ItemKind: "artists", MediaType: string(MediaType)}
	artists, err := client.Artists(ctx)
	if err != nil {
		return arrmap.PreviewSource{}, err
	}
	for _, artist := range artists {
		source.Items = append(source.Items, arrmap.PreviewItem{RootFolder: artist.RootFolderPath, Monitored: artist.Monitored,
			ProfileID: artist.QualityProfileID, Provider: ProviderArtist, ExternalID: artist.ForeignArtistID, Person: true})
	}
	names := arrmap.ProfileNames(ctx, client.QualityProfiles, func(profile arrimport.LidarrQualityProfile) (int64, string) {
		return profile.ID, profile.Name
	})
	return arrmap.ReadPreview(ctx, source, client, names)
}

// albumWanted is the albums option: every album, those the *Arr lacks a track of, or those it holds whole.
func albumWanted(album arrimport.LidarrAlbum, which string) bool {
	switch which {
	case "missing":
		return !album.Complete()
	case "complete":
		return album.Complete()
	default:
		return true
	}
}
