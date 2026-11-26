package tv

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/media/works"
)

var _ works.Describer = CatalogSource{}

// previewCrew are the series-wide crew roles a preview names beside the cast.
var previewCrew = []string{RoleCreator, RoleShowrunner, RoleDirector, RoleWriter}

// previewCastLimit caps the cast a preview lists; the library item page holds the whole of it.
const previewCastLimit = 24

// Describe implements works.Describer: the series as the sources merge it, nothing stored.
func (c CatalogSource) Describe(ctx context.Context, request media.DescribeRequest) (media.WorkPreview, error) {
	id, valid := ParseID(request.ExternalID)
	if (request.Provider != ProviderTVDB && request.Provider != ProviderTMDB) || !valid {
		return media.WorkPreview{}, fmt.Errorf("%w: only a numeric tvdb or tmdb id can be described", media.ErrInvalidAdd)
	}
	ids := IDs{TVDB: id}
	if request.Provider == ProviderTMDB {
		ids = IDs{TMDB: id}
	}
	series, err := c.Service.FetchFor(ctx, request.CategoryID, ids)
	if errors.Is(err, ErrNoSource) {
		return media.WorkPreview{}, fmt.Errorf("%w: %w", media.ErrSourceUnavailable, err)
	}
	if err != nil {
		return media.WorkPreview{}, err
	}
	return PreviewOf(series), nil
}

// PreviewOf is a merged series as a preview.
func PreviewOf(series Series) media.WorkPreview {
	preview := media.WorkPreview{
		Title: series.Title, Subtitle: series.Network, Overview: series.Overview, Status: string(series.Status),
		Year: int64(series.Year), Genres: series.Genres,
	}
	for _, image := range series.Images {
		if image.Kind == "poster" {
			preview.PosterURL = image.URL
			break
		}
	}
	addFact := func(key, value string) {
		if value != "" {
			preview.Facts = append(preview.Facts, media.PreviewFact{Key: key, Value: value})
		}
	}
	addFact(media.FactNetwork, series.Network)
	if series.RuntimeMin > 0 {
		addFact(media.FactRuntimeMin, strconv.Itoa(series.RuntimeMin))
	}
	addFact(media.FactCertification, series.ContentRating)
	addFact(media.FactLanguage, series.OriginalLanguage)
	addFact(media.FactFirstAired, series.FirstAired)
	for _, rating := range series.Ratings {
		preview.Ratings = append(preview.Ratings, media.PreviewRating{Provider: rating.Provider, Score: rating.Score, Votes: int64(rating.Votes)})
	}
	preview.People = previewPeople(series)
	preview.Groups = previewGroups(series.Episodes)
	return preview
}

func previewPeople(series Series) []media.PreviewPerson {
	var people []media.PreviewPerson
	cast := 0
	for _, credit := range series.Credits {
		if credit.Season != 0 || credit.Episode != 0 {
			continue
		}
		switch {
		case credit.Role == RoleActor && cast < previewCastLimit:
			cast++
		case slices.Contains(previewCrew, credit.Role):
		default:
			continue
		}
		people = append(people, media.PreviewPerson{Name: credit.Name, Role: credit.Role, Character: credit.Character, ImageURL: credit.Image})
	}
	if len(people) == 0 {
		for _, member := range series.Cast[:min(len(series.Cast), previewCastLimit)] {
			people = append(people, media.PreviewPerson{Name: member.Name, Role: RoleActor, Character: member.Character, ImageURL: member.Image})
		}
	}
	return people
}

func previewGroups(episodes []Episode) []media.PreviewGroup {
	sorted := slices.Clone(episodes)
	slices.SortStableFunc(sorted, func(a, b Episode) int {
		return cmp.Or(cmp.Compare(a.Season, b.Season), cmp.Compare(a.Number, b.Number))
	})
	var groups []media.PreviewGroup
	for _, episode := range sorted {
		if len(groups) == 0 || groups[len(groups)-1].Number != episode.Season {
			groups = append(groups, media.PreviewGroup{Number: episode.Season})
		}
		last := &groups[len(groups)-1]
		last.Parts = append(last.Parts, media.PreviewPart{Number: strconv.Itoa(episode.Number), Title: episode.Title, Date: episode.AirDate, DurationSec: episode.RuntimeMin * 60})
	}
	return groups
}
