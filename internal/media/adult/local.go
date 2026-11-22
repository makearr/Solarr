package adult

import (
	"context"
	"strings"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
)

// FromServer is what a media server of the person's own holds of a scene; the merge ranks it after every other source.
func FromServer(item mediaserver.Item, source string) Scene {
	scene := Scene{
		IDs:   IDs{StashDB: item.IDs["stashdb"], ThePornDB: item.IDs["theporndb"], FansDB: item.IDs["fansdb"]},
		Title: item.Title, Overview: item.Overview, Date: item.Date, Year: item.Year, Code: item.IDs["code"], Tags: item.Tags, Sources: []string{source},
	}
	scene.DurationSec = int(item.RuntimeMS / 1000)
	if len(item.Organisations) > 0 {
		scene.Studio = item.Organisations[0]
	}
	order := 0
	for _, person := range item.People {
		switch person.Kind {
		case "actor":
			scene.Credits = append(scene.Credits, Credit{Name: person.Name, Role: RolePerformer, Order: order})
			order++
		case "director":
			scene.Director = person.Name
		}
	}
	return scene
}

// askLocal asks the media server of the source's kind the library is mapped to, by a stash-box id the scene carries, else by its title.
func (s Service) askLocal(ctx context.Context, categoryID int64, source string, ids IDs, hint Hint) (Scene, bool, error) {
	if s.Local == nil || categoryID == 0 {
		return Scene{}, false, nil
	}
	known := map[string]string{"stashdb": ids.StashDB, "theporndb": ids.ThePornDB, "fansdb": ids.FansDB}
	item, found, err := s.Local.Ask(ctx, categoryID, strings.TrimPrefix(source, settings.LocalSourcePrefix), "adult", mediaserver.Question{IDs: known, Title: hint.Title})
	if err != nil || !found {
		return Scene{}, false, err
	}
	scene := FromServer(item, source)
	// An id from a file or another provider is never replaced by the server's.
	for _, kept := range []struct {
		held string
		own  *string
	}{{ids.StashDB, &scene.IDs.StashDB}, {ids.ThePornDB, &scene.IDs.ThePornDB}, {ids.FansDB, &scene.IDs.FansDB}} {
		if kept.held != "" {
			*kept.own = ""
		}
	}
	return scene, true, nil
}
