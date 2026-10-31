package adult

import (
	"context"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
)

const (
	// newSceneWindow lets a follow also catch a scene that came out shortly before the follow was made.
	newSceneWindow = 14 * 24 * time.Hour
	// maxPerFollow bounds what one run adds for one follow, so a prolific performer never floods a library; a
	// back-catalogue follow is allowed more because it asked for it.
	maxPerFollow        = 5
	maxPerBackCatalogue = 25
	// pageSize is how many of a subject's newest scenes one run looks at.
	pageSize = 50
)

// FollowPeople is the people store as a follow needs it.
type FollowPeople interface {
	ExternalIDFor(ctx context.Context, personID int64, provider string) (string, bool, error)
}

// FollowStudios is the organisation store as a follow needs it.
type FollowStudios interface {
	OrganisationExternalID(ctx context.Context, organisationID int64, provider string) (string, bool, error)
}

// Following is the adult part of a follow, Whisparr's monitored performers and studios: a stash-box server's newest
// scenes of the subject, those out since the follow was made, or every one for a back-catalogue follow.
type Following struct {
	Service Service
	Catalog Catalog
	People  FollowPeople
	Studios FollowStudios
	Now     func() time.Time
	// Held says the library holds a scene by its ids; nil asks the service.
	Held func(ctx context.Context, ids IDs) (int64, bool)
}

// FollowLimit implements media.Following.
func (Following) FollowLimit(follow media.Follow) int {
	if follow.BackCatalogue {
		return maxPerBackCatalogue
	}
	return maxPerFollow
}

// FollowCandidates implements media.Following.
func (f Following) FollowCandidates(ctx context.Context, follow media.Follow) ([]media.FollowCandidate, error) {
	since := time.Unix(follow.CreatedAt, 0).UTC().Add(-newSceneWindow).Format("2006-01-02")
	if follow.BackCatalogue {
		since = ""
	}
	held := f.Held
	if held == nil {
		held = f.Service.WorkByIDs
	}
	var candidates []media.FollowCandidate
	for _, name := range serverOrder {
		server, ok := f.Service.Servers[name]
		if !ok || server == nil || !server.Configured() {
			continue
		}
		filter, found, err := f.filterFor(ctx, follow, name)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		scenes, err := server.ScenesOf(ctx, filter, 1, pageSize)
		if err != nil {
			f.Service.warn(name, "follow", err)
			continue
		}
		for _, scene := range scenes {
			day := validDay(scene.ReleaseDate)
			if since != "" && (day == "" || day < since) || day > f.Now().Format("2006-01-02") {
				continue
			}
			candidate := media.FollowCandidate{Provider: name, ExternalID: scene.ID, Title: scene.Title, Year: int64(yearOf(day)), Role: roleOf(follow)}
			if len(scene.Images) > 0 {
				candidate.PosterURL = scene.Images[0].URL
			}
			_, candidate.Held = held(ctx, namedByProvider(name, scene.ID))
			candidates = append(candidates, candidate)
		}
		// One server's list is the follow's: asking a second would add the same scenes under other ids.
		return candidates, nil
	}
	return nil, nil
}

// filterFor is the subject's id at the server, false when the library holds the subject from another server only.
func (f Following) filterFor(ctx context.Context, follow media.Follow, server string) (stashbox.SceneFilter, bool, error) {
	switch follow.SubjectType {
	case "person":
		id, found, err := f.People.ExternalIDFor(ctx, follow.SubjectID, performerProvider(server))
		return stashbox.SceneFilter{PerformerID: id}, found, err
	case "organisation":
		id, found, err := f.Studios.OrganisationExternalID(ctx, follow.SubjectID, studioProvider(server))
		return stashbox.SceneFilter{StudioID: id}, found, err
	}
	return stashbox.SceneFilter{}, false, nil
}

func roleOf(follow media.Follow) string {
	if follow.SubjectType == "person" {
		return RolePerformer
	}
	return ""
}

func namedByProvider(provider, id string) IDs {
	ids, _ := NamedIDs(provider, id)
	return ids
}

// AddFollowed implements media.Following: added as the add dialog adds it, monitored, but never past an exclusion.
func (f Following) AddFollowed(ctx context.Context, follow media.Follow, candidate media.FollowCandidate) error {
	catalog := works.Catalog{Source: catalogSource{f.Catalog.Service}, Categories: f.Catalog.Categories, DefaultProfileID: f.Catalog.DefaultProfileID}
	_, err := catalog.AddWork(ctx, media.AddRequest{
		CategoryID: follow.CategoryID, Kind: MediaType, Provider: candidate.Provider, ExternalID: candidate.ExternalID, Monitored: true,
	})
	if err != nil {
		return fmt.Errorf("adult: add followed scene: %w", err)
	}
	return nil
}
