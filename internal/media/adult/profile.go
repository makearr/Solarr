package adult

import (
	"context"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/stashbox"
)

// maxOtherScenes bounds the scenes a performer's page lists beside the ones the library holds.
const maxOtherScenes = 60

// PerformerScenes is the adult type's part of a performer's page: their scenes at a server that the library lacks.
type PerformerScenes struct {
	Service Service
	People  FollowPeople
	// Held says the library holds a scene by its ids; nil asks the service.
	Held func(ctx context.Context, ids IDs) (int64, bool)
}

// KnownFor implements media.Profiling: the performer's newest released scenes the library does not hold.
func (p PerformerScenes) KnownFor(ctx context.Context, personID, _ int64) ([]media.KnownFor, error) {
	for _, name := range serverOrder {
		server, ok := p.Service.Servers[name]
		if !ok || server == nil || !server.Configured() {
			continue
		}
		id, found, err := p.People.ExternalIDFor(ctx, personID, performerProvider(name))
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		scenes, err := server.ScenesOf(ctx, stashbox.SceneFilter{PerformerID: id}, 1, maxOtherScenes)
		if err != nil {
			p.Service.warn(name, "performer scenes", err)
			continue
		}
		return p.missing(ctx, name, scenes), nil
	}
	return nil, nil
}

func (p PerformerScenes) missing(ctx context.Context, server string, scenes []stashbox.Scene) []media.KnownFor {
	held := p.Held
	if held == nil {
		held = p.Service.WorkByIDs
	}
	var others []media.KnownFor
	for _, scene := range scenes {
		if _, held := held(ctx, namedByProvider(server, scene.ID)); held {
			continue
		}
		day := validDay(scene.ReleaseDate)
		other := media.KnownFor{Provider: server, ExternalID: scene.ID, Kind: MediaType, Title: scene.Title, Year: int64(yearOf(day)), Role: RolePerformer}
		if len(scene.Images) > 0 {
			other.PosterURL = scene.Images[0].URL
		}
		others = append(others, other)
	}
	return others
}
