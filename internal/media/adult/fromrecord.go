package adult

import (
	"context"
	"strings"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

// ConsumesRecords implements media.RecordConsuming.
func (Module) ConsumesRecords() bool { return true }

var _ media.RecordConsuming = Module{}

// RecordProvider is a definition-file provider as the adult service asks it.
type RecordProvider interface {
	ByID(ctx context.Context, namespace, id string) (specprovider.Record, error)
	Search(ctx context.Context, query specprovider.Query) ([]specprovider.Record, error)
}

func (s Service) recordProvider(key string) RecordProvider {
	if s.Records == nil {
		return nil
	}
	return s.Records(key)
}

// FromRecord turns a definition's record into a scene; a definition's own id is not kept, only a stash-box id it carries.
func FromRecord(key string, record specprovider.Record) Scene {
	scene := Scene{
		Title: strings.TrimSpace(record.Title), Overview: strings.TrimSpace(record.Overview), Date: validDay(record.Date),
		DurationSec: record.RuntimeSeconds, Sources: []string{key},
	}
	scene.Year = yearOf(scene.Date)
	if scene.Year == 0 {
		scene.Year = record.Year
	}
	scene.IDs = IDs{StashDB: record.IDs["stashdb"], ThePornDB: record.IDs["theporndb"], FansDB: record.IDs["fansdb"]}
	for _, tag := range append(append([]string{}, record.Tags...), record.Genres...) {
		if name := strings.TrimSpace(tag); name != "" && len(scene.Tags) < maxTags {
			scene.Tags = append(scene.Tags, name)
		}
	}
	scene.URLs = append(scene.URLs, record.Links...)
	for _, organisation := range record.Organisations {
		scene.Studio = organisation.Name
		break
	}
	for _, image := range record.Images {
		if len(scene.Images) < maxImages {
			scene.Images = append(scene.Images, Image{Kind: "poster", URL: image.URL, Source: key})
		}
	}
	for _, person := range record.People {
		name := strings.TrimSpace(person.Name)
		switch {
		case name == "":
		case person.Role == "director":
			scene.Director = name
			scene.Credits = append(scene.Credits, Credit{Provider: ProviderPerformerName, ExternalID: NameKey(name), Name: name, Role: RoleDirector, Order: len(scene.Credits)})
		default:
			scene.Credits = append(scene.Credits, Credit{Provider: ProviderPerformerName, ExternalID: NameKey(name), Name: name, Role: RolePerformer, Order: len(scene.Credits)})
		}
	}
	return scene
}

// askRecord reads a scene from a definition by a stash-box id Solarr holds, else by the title it was told.
func (s Service) askRecord(ctx context.Context, key string, ids IDs, hint Hint) (Scene, bool, error) {
	provider := s.recordProvider(key)
	if provider == nil {
		return Scene{}, false, nil
	}
	var failure error
	for _, own := range []struct{ namespace, id string }{{"stashdb", ids.StashDB}, {"theporndb", ids.ThePornDB}, {"fansdb", ids.FansDB}} {
		if own.id == "" {
			continue
		}
		record, err := provider.ByID(ctx, own.namespace, own.id)
		if err != nil {
			failure = err
			continue
		}
		if record.Title != "" {
			return FromRecord(key, record), true, nil
		}
	}
	if hint.Title == "" {
		return Scene{}, false, failure
	}
	found, err := provider.Search(ctx, specprovider.Query{Title: hint.Title, Year: yearText(hint.Date)})
	if err != nil {
		return Scene{}, false, err
	}
	for _, record := range found {
		if record.Title != "" && NameKey(record.Title) == NameKey(hint.Title) {
			return FromRecord(key, record), true, nil
		}
	}
	return Scene{}, false, failure
}

func yearText(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return ""
}
