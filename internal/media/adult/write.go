package adult

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

// releaseKind is the work_date kind the planner, the calendar and "Not out yet" read; one date per scene.
const releaseKind = "release"

// Fields of a scene other than the base ones, as stored in work_field.
const (
	FieldStatus   = "status"
	FieldCode     = "code"
	FieldDirector = "director"
	FieldURLs     = "urls"
	FieldImages   = "images"
	FieldRelease  = "release_date"
	FieldStudio   = "studio_name"
	FieldNetwork  = "network_name"
)

// GenreTree holds the tags of scenes. The servers' tags are a community vocabulary, so every one becomes a tag and the
// tree starts empty.
var GenreTree = media.GenreTree{Key: "adult", Label: "Tags", Version: 1}

// ToWrite turns a merged scene into what the library stores; now decides whether it is out yet.
func ToWrite(scene Scene, now time.Time) library.SeriesWrite {
	var yearPtr, runtimePtr *int64
	if scene.Year > 0 {
		year := int64(scene.Year)
		yearPtr = &year
	}
	if scene.DurationSec > 0 {
		runtime := int64(scene.DurationSec) * 1000
		runtimePtr = &runtime
	}
	source := ""
	if len(scene.Sources) > 0 {
		source = scene.Sources[0]
	}
	write := library.SeriesWrite{
		Work:             library.WorkIdentity{MediaType: MediaType, CanonicalTitle: scene.Title, SortTitle: SortTitle(scene.Title)},
		Year:             yearPtr,
		RuntimeMs:        runtimePtr,
		Description:      scene.Overview,
		IDs:              idWrites(scene.IDs),
		Genres:           scene.Tags,
		GenreSource:      source,
		GenreTree:        GenreTree.Key,
		Network:          scene.Studio,
		OrganisationKind: "studio",
		CertificationAge: -1,
		Fields:           fieldWrites(scene, source, now),
		LeadRole:         RolePerformer,
	}
	// A network that names the primary studio adds its id and logo to it (library.SeriesWrite.MoreNetworks).
	if scene.StudioID != "" {
		write.MoreNetworks = append(write.MoreNetworks, library.SeriesNetworkWrite{Name: scene.Studio, Provider: scene.StudioProvider, ExternalID: scene.StudioID, LogoURL: scene.StudioLogo})
	}
	if scene.Network != "" && scene.Network != scene.Studio {
		network := library.SeriesNetworkWrite{Name: scene.Network, Provider: ProviderStudio, ExternalID: NameKey(scene.Network)}
		if scene.NetworkID != "" {
			network.Provider, network.ExternalID = scene.StudioProvider, scene.NetworkID
		}
		write.MoreNetworks = append(write.MoreNetworks, network)
	}
	if scene.Date != "" {
		write.Dates = append(write.Dates, library.SeriesDateWrite{Kind: releaseKind, Date: scene.Date, Source: source})
	}
	for _, credit := range scene.Credits {
		provider, externalID := credit.Provider, credit.ExternalID
		if externalID == "" {
			provider, externalID = ProviderPerformerName, NameKey(credit.Name)
		}
		if externalID == "" {
			continue
		}
		write.Credits = append(write.Credits, library.SeriesCreditWrite{
			Provider: provider, ExternalID: externalID, Name: strings.TrimSpace(credit.Name), ImageURL: credit.Image,
			Role: credit.Role, Character: credit.As, Ord: credit.Order,
		})
	}
	return write
}

// idWrites are the provider ids a scene is stored under.
func idWrites(ids IDs) []library.ExternalIDWrite {
	var writes []library.ExternalIDWrite
	for _, entry := range []struct{ provider, id string }{
		{ProviderStashDB, ids.StashDB}, {ProviderThePornDB, ids.ThePornDB}, {ProviderFansDB, ids.FansDB},
	} {
		if entry.id != "" {
			writes = append(writes, library.ExternalIDWrite{Provider: entry.provider, ExternalID: entry.id})
		}
	}
	return writes
}

// idsOf reads a stored work's ids back.
func idsOf(provider map[string]string) IDs {
	return IDs{StashDB: provider[ProviderStashDB], ThePornDB: provider[ProviderThePornDB], FansDB: provider[ProviderFansDB]}
}

func fieldWrites(scene Scene, source string, now time.Time) []library.SeriesFieldWrite {
	var fields []library.SeriesFieldWrite
	add := func(field, value string) {
		if strings.TrimSpace(value) != "" {
			fields = append(fields, library.SeriesFieldWrite{Field: field, Provider: source, Value: value})
		}
	}
	add(FieldStatus, string(scene.StatusOn(now)))
	add(FieldCode, scene.Code)
	add(FieldDirector, scene.Director)
	add(FieldRelease, scene.Date)
	add(FieldStudio, scene.Studio)
	add(FieldNetwork, scene.Network)
	for field, values := range map[string]any{FieldURLs: scene.URLs, FieldImages: scene.Images} {
		if encoded, err := json.Marshal(values); err == nil && string(encoded) != "null" && string(encoded) != "[]" {
			add(field, string(encoded))
		}
	}
	return fields
}

// SortTitle lowercases a title, collapses whitespace and drops a leading article.
func SortTitle(title string) string {
	lowered := strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(title))), " ")
	for _, article := range []string{"the ", "a ", "an "} {
		if rest, ok := strings.CutPrefix(lowered, article); ok {
			return rest
		}
	}
	return lowered
}

// yearString is a year as text, empty when unknown.
func yearString(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}
