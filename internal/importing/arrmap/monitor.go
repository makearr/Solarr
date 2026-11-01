package arrmap

import (
	"context"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// Skipped is the tally of an item a monitoring-only import found no counterpart of here: nothing was added.
var Skipped = library.ArrImportCounts{ItemsParked: 1}

// Found tallies one monitored item: matched when Solarr held it, else skipped.
func Found(held bool, err error) (library.ArrImportCounts, error) {
	switch {
	case err != nil:
		return library.ArrImportCounts{}, err
	case held:
		return library.ArrImportCounts{ArtistsMatched: 1}, nil
	}
	return Skipped, nil
}

// Monitors sets the monitoring of what Solarr holds by a provider's id; it adds nothing.
type Monitors struct {
	Series        library.Series
	Desired       library.Desired
	People        library.People
	Subscriptions library.Subscriptions
	Organisations library.Organisations
	Registry      *media.Registry
	Clock         clock.Clock
}

// MonitorChoice is the monitor option of a media type that means "monitored" (its widest) or "not": the type's own words
// for it, from its descriptor.
func MonitorChoice(descriptor media.Descriptor, monitored bool) (string, bool) {
	if len(descriptor.MonitorOptions) == 0 {
		return "", false
	}
	if !monitored {
		for _, option := range descriptor.MonitorOptions {
			if option.Key == "none" {
				return option.Key, true
			}
		}
		return "", false
	}
	for _, preferred := range []string{"all", "scene_only", "movie_only", "book_only"} {
		if slices.ContainsFunc(descriptor.MonitorOptions, func(option media.Choice) bool { return option.Key == preferred }) {
			return preferred, true
		}
	}
	for _, option := range descriptor.MonitorOptions {
		if option.Key != "none" {
			return option.Key, true
		}
	}
	return "", false
}

// Work sets the monitoring of the work of the media type that the provider names by id, through the type's own monitor
// option, else item by item; false when Solarr holds no such work.
func (m Monitors) Work(ctx context.Context, mediaType, provider, externalID string, monitored bool) (int64, bool, error) {
	if externalID == "" || externalID == "0" {
		return 0, false, nil
	}
	workID, held, err := m.Series.WorkByProviderID(ctx, mediaType, provider, externalID)
	if err != nil || !held {
		return 0, false, err
	}
	items, err := m.Desired.AlbumItemsForWork(ctx, workID)
	if err != nil || len(items) == 0 {
		return 0, false, err
	}
	if m.Registry != nil {
		if module, ok := m.Registry.Module(media.Key(mediaType)); ok {
			if monitoring, ok := module.(media.Monitoring); ok {
				if option, known := MonitorChoice(module.Descriptor(), monitored); known {
					return workID, true, monitoring.ApplyMonitorOption(ctx, items[0].CategoryID, workID, option, false)
				}
			}
		}
	}
	for _, item := range items {
		if err := m.Desired.SetItemMonitored(ctx, item.ID, monitored); err != nil {
			return workID, true, err
		}
	}
	return workID, true, nil
}

// Person follows the person the provider names by id in the media type's libraries, or stops following them; false when
// Solarr holds no such person.
func (m Monitors) Person(ctx context.Context, provider, externalID, mediaType, role string, categoryID int64, monitored bool) (int64, bool, error) {
	if externalID == "" {
		return 0, false, nil
	}
	personID, held, err := m.People.FindByExternalID(ctx, provider, externalID)
	if err != nil || !held {
		return 0, false, err
	}
	if monitored {
		_, err := m.People.Subscribe(ctx, m.Clock.Now(), personID, 0, library.SubscriptionRequest{MediaTypes: []string{mediaType}, Roles: []string{role}, CategoryID: categoryID})
		return personID, true, err
	}
	return personID, true, m.unfollow(ctx, library.Subject{Type: "person", ID: personID})
}

// Organisation is Person for a studio.
func (m Monitors) Organisation(ctx context.Context, provider, externalID, mediaType string, categoryID int64, monitored bool) (int64, bool, error) {
	if externalID == "" {
		return 0, false, nil
	}
	organisationID, held, err := m.Organisations.FindByExternalID(ctx, provider, externalID)
	if err != nil || !held {
		return 0, false, err
	}
	subject := library.Subject{Type: "organisation", ID: organisationID}
	if monitored {
		_, err := m.Subscriptions.Follow(ctx, m.Clock.Now(), subject, 0, library.FollowRequest{CategoryID: categoryID, MediaType: mediaType})
		return organisationID, true, err
	}
	return organisationID, true, m.unfollow(ctx, subject)
}

func (m Monitors) unfollow(ctx context.Context, subject library.Subject) error {
	follows, err := m.Subscriptions.FollowsOf(ctx, subject, 0)
	if err != nil {
		return err
	}
	for _, follow := range follows {
		if err := m.Subscriptions.Unfollow(ctx, subject, 0, follow.CategoryID); err != nil {
			return err
		}
	}
	return nil
}

// The *Arr's own words for what a link is of.
const (
	LinkArtist    = "artist"
	LinkAlbum     = "album"
	LinkSeries    = "series"
	LinkMovie     = "movie"
	LinkScene     = "scene"
	LinkPerformer = "performer"
	LinkStudio    = "studio"
)
