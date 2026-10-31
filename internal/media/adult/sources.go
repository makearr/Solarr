package adult

import (
	"cmp"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/stashbox"
	"github.com/makearr/Solarr/internal/metadata/theporndb"
	"github.com/makearr/Solarr/internal/metadata/whisparr"
)

// Source names, as listed in Scene.Sources.
const (
	sourceWhisparr = "whisparr"
	maxTags        = 40
	maxImages      = 6
)

// FromStashBox turns a stash-box server's scene into a scene; provider is the server's name.
func FromStashBox(provider string, in stashbox.Scene) Scene {
	scene := Scene{
		Title: strings.TrimSpace(in.Title), Overview: strings.TrimSpace(in.Details), Date: validDay(in.ReleaseDate), DurationSec: in.Duration,
		Director: strings.TrimSpace(in.Director), Code: strings.TrimSpace(in.Code), Sources: []string{provider},
	}
	scene.Year = yearOf(scene.Date)
	setID(&scene.IDs, provider, in.ID)
	if in.Studio != nil {
		scene.Studio, scene.StudioID, scene.StudioProvider = in.Studio.Name, in.Studio.ID, studioProvider(provider)
		if in.Studio.Parent != nil {
			scene.Network, scene.NetworkID = in.Studio.Parent.Name, in.Studio.Parent.ID
		}
		if len(in.Studio.Images) > 0 {
			scene.StudioLogo = in.Studio.Images[0].URL
		}
	}
	for _, tag := range in.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" && len(scene.Tags) < maxTags {
			scene.Tags = append(scene.Tags, name)
		}
	}
	for _, link := range in.URLs {
		scene.URLs = append(scene.URLs, link.URL)
	}
	for _, image := range in.Images {
		if len(scene.Images) < maxImages {
			scene.Images = append(scene.Images, Image{Kind: "poster", URL: image.URL, Source: provider})
		}
	}
	for index, appearance := range in.Performers {
		performer := appearance.Performer
		credit := Credit{Provider: performerProvider(provider), ExternalID: performer.ID, Name: strings.TrimSpace(performer.Name), Gender: performer.Gender,
			Role: RolePerformer, Order: index}
		if appearance.As != "" && NameKey(appearance.As) != NameKey(performer.Name) {
			credit.As = appearance.As
		}
		if len(performer.Images) > 0 {
			credit.Image = performer.Images[0].URL
		}
		if credit.Name != "" {
			scene.Credits = append(scene.Credits, credit)
		}
	}
	if scene.Director != "" {
		scene.Credits = append(scene.Credits, Credit{Provider: ProviderPerformerName, ExternalID: NameKey(scene.Director), Name: scene.Director, Role: RoleDirector, Order: len(scene.Credits)})
	}
	for _, print := range in.Fingerprints {
		scene.Fingerprint = append(scene.Fingerprint, Fingerprint{Hash: print.Hash, Algorithm: print.Algorithm, DurationSec: print.Duration})
	}
	return scene
}

// FromWhisparr turns Whisparr's server's scene into a scene; its ids are ThePornDB's.
func FromWhisparr(in whisparr.Scene) Scene {
	scene := Scene{
		IDs: IDs{ThePornDB: in.ForeignGUID}, Title: strings.TrimSpace(in.Title), Overview: strings.TrimSpace(in.Overview), Date: validDay(in.ReleaseDate),
		DurationSec: in.Duration * 60, Tags: slices.Clone(in.Genres), Sources: []string{sourceWhisparr},
	}
	scene.Year = cmp.Or(yearOf(scene.Date), in.Year())
	for _, image := range in.Images {
		if len(scene.Images) < maxImages {
			scene.Images = append(scene.Images, Image{Kind: "poster", URL: image.URL, Source: sourceWhisparr})
		}
	}
	for index, credit := range in.Credits {
		entry := Credit{Provider: performerProvider(ProviderThePornDB), ExternalID: credit.ForeignGUID, Name: strings.TrimSpace(credit.Name), Gender: strings.ToUpper(credit.Gender),
			Role: RolePerformer, Order: index}
		if len(credit.Images) > 0 {
			entry.Image = credit.Images[0].URL
		}
		if entry.Name != "" {
			scene.Credits = append(scene.Credits, entry)
		}
	}
	return scene
}

func setID(ids *IDs, provider, id string) {
	switch provider {
	case ProviderStashDB:
		ids.StashDB = id
	case ProviderThePornDB:
		ids.ThePornDB = id
	case ProviderFansDB:
		ids.FansDB = id
	}
}

// studioProvider is the key a server's studio ids are stored under.
func studioProvider(server string) string { return server + ":studio" }

// performerProvider is the key a server's performer ids are stored under.
func performerProvider(server string) string { return server + ":performer" }

// validDay is a date as yyyy-mm-dd, empty when the source gave a year or month only.
func validDay(date string) string {
	if len(date) >= 10 && date[4] == '-' && date[7] == '-' {
		return date[:10]
	}
	return ""
}

func yearOf(day string) int {
	if len(day) < 4 {
		return 0
	}
	year, _ := strconv.Atoi(day[:4])
	return year
}

// Merge fills what first lacks from second; first wins wherever it holds a value. The credits stay one source's: the
// same performer has a different id at each server, so mixing two lists would credit them twice.
func Merge(first, second Scene) Scene {
	merged := first
	merged.IDs = IDs{
		StashDB: cmp.Or(first.IDs.StashDB, second.IDs.StashDB), ThePornDB: cmp.Or(first.IDs.ThePornDB, second.IDs.ThePornDB),
		FansDB: cmp.Or(first.IDs.FansDB, second.IDs.FansDB),
	}
	merged.Title = cmp.Or(first.Title, second.Title)
	merged.Overview = cmp.Or(first.Overview, second.Overview)
	merged.Date = cmp.Or(first.Date, second.Date)
	merged.Year = cmp.Or(first.Year, second.Year)
	merged.DurationSec = cmp.Or(first.DurationSec, second.DurationSec)
	merged.Studio = cmp.Or(first.Studio, second.Studio)
	if first.Studio == "" || first.Studio == second.Studio {
		merged.StudioID, merged.StudioProvider = cmp.Or(first.StudioID, second.StudioID), cmp.Or(first.StudioProvider, second.StudioProvider)
		merged.Network, merged.NetworkID = cmp.Or(first.Network, second.Network), cmp.Or(first.NetworkID, second.NetworkID)
		merged.StudioLogo = cmp.Or(first.StudioLogo, second.StudioLogo)
	}
	merged.Director = cmp.Or(first.Director, second.Director)
	merged.Code = cmp.Or(first.Code, second.Code)
	merged.Tags = unionNames(first.Tags, second.Tags, maxTags)
	merged.URLs = unionNames(first.URLs, second.URLs, 20)
	merged.Images = slices.Concat(first.Images, second.Images)
	if len(merged.Images) > maxImages*2 {
		merged.Images = merged.Images[:maxImages*2]
	}
	if len(first.Performers()) == 0 {
		merged.Credits = second.Credits
	}
	merged.Fingerprint = append(slices.Clone(first.Fingerprint), second.Fingerprint...)
	merged.Sources = append(slices.Clone(first.Sources), second.Sources...)
	return merged
}

// unionNames is first then what second adds, compared as names.
func unionNames(first, second []string, limit int) []string {
	out := slices.Clone(first)
	seen := map[string]bool{}
	for _, name := range out {
		seen[NameKey(name)] = true
	}
	for _, name := range second {
		if key := NameKey(name); !seen[key] && len(out) < limit {
			seen[key] = true
			out = append(out, name)
		}
	}
	return out
}

// FromMovie turns one of ThePornDB's films into a scene: the studio is its site and the network that site's.
func FromMovie(in theporndb.Movie) Scene {
	scene := Scene{
		IDs: IDs{ThePornDB: in.ID}, Title: strings.TrimSpace(in.Title), Overview: strings.TrimSpace(in.Description), Date: validDay(in.Date),
		DurationSec: in.Duration, Sources: []string{ProviderThePornDB},
	}
	scene.Year = yearOf(scene.Date)
	if in.URL != "" {
		scene.URLs = []string{in.URL}
	}
	if site := in.Site; site != nil {
		scene.Studio, scene.StudioID, scene.StudioProvider, scene.StudioLogo = site.Name, site.UUID, studioProvider(ProviderThePornDB), site.Logo
		if site.Network != nil {
			scene.Network, scene.NetworkID = site.Network.Name, site.Network.UUID
		}
	}
	for _, tag := range in.Tags {
		if name := strings.TrimSpace(tag.Name); name != "" && len(scene.Tags) < maxTags {
			scene.Tags = append(scene.Tags, name)
		}
	}
	for _, url := range []string{in.Image, in.BackImage, in.Poster} {
		if url != "" && len(scene.Images) < maxImages {
			scene.Images = append(scene.Images, Image{Kind: "poster", URL: url, Source: ProviderThePornDB})
		}
	}
	for index, performer := range in.Performers {
		id, name, image, gender := performer.Canonical()
		if name = strings.TrimSpace(name); name != "" {
			scene.Credits = append(scene.Credits, Credit{Provider: performerProvider(ProviderThePornDB), ExternalID: id, Name: name,
				Gender: strings.ToUpper(strings.ReplaceAll(gender, " ", "_")), Image: image, Role: RolePerformer, Order: index})
		}
	}
	for _, director := range in.Directors {
		if name := strings.TrimSpace(director.Name); name != "" {
			scene.Director = cmp.Or(scene.Director, name)
			scene.Credits = append(scene.Credits, Credit{Provider: ProviderPerformerName, ExternalID: NameKey(name), Name: name, Role: RoleDirector, Order: len(scene.Credits)})
		}
	}
	return scene
}
