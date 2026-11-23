package tv

import (
	"context"
	"strconv"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

// ConsumesRecords implements media.RecordConsuming.
func (Module) ConsumesRecords() bool { return true }

var _ media.RecordConsuming = Module{}

// RecordProvider is a definition-file provider as the TV service asks it.
type RecordProvider interface {
	ByID(ctx context.Context, namespace, id string) (specprovider.Record, error)
}

func (s Service) recordProvider(key string) RecordProvider {
	if s.Records == nil {
		return nil
	}
	return s.Records(key)
}

// FromRecord turns a definition's record into a series: series-level fields only, never episodes, whose numbering a definition
// has no way to vouch for.
func FromRecord(key string, record specprovider.Record) Series {
	series := Series{
		Title: record.Title, Overview: record.Overview, Year: record.Year, FirstAired: dayOf(record.Date), LastAired: dayOf(record.EndDate),
		Genres: append([]string{}, record.Genres...), Keywords: append([]string{}, record.Tags...), ContentRating: record.AgeRating,
		OriginalLanguage: record.Language, OriginalCountry: record.Country, Status: statusOfRecord(record.Status), Sources: []string{key},
	}
	if record.RuntimeSeconds > 0 {
		series.RuntimeMin = record.RuntimeSeconds / 60
	}
	for _, alias := range append([]string{record.OriginalTitle}, record.AltTitles...) {
		if alias != "" && alias != record.Title {
			series.Aliases = appendDistinct(series.Aliases, alias)
		}
	}
	series.IDs.IMDb = record.IDs["imdb"]
	series.IDs.TVDB, _ = strconv.ParseInt(record.IDs["tvdb"], 10, 64)
	series.IDs.TMDB, _ = strconv.ParseInt(record.IDs["tmdb"], 10, 64)
	for _, organisation := range record.Organisations {
		series.Network = organisation.Name
		break
	}
	for _, image := range record.Images {
		series.Images = append(series.Images, Image{Kind: imageKind(image.Kind), URL: image.URL, Source: key})
	}
	for _, person := range record.People {
		if person.Role == "actor" || person.Role == "" {
			series.Cast = append(series.Cast, Person{Name: person.Name})
		}
	}
	if record.Rating.Value > 0 {
		series.Rating, series.RatingVotes = record.Rating.Value, record.Rating.Votes
	}
	return series
}

func dayOf(date string) string {
	if len(date) >= 10 {
		return date[:10]
	}
	return ""
}

func statusOfRecord(status string) Status {
	switch status {
	case "ended", "cancelled", "canceled":
		return StatusEnded
	case "continuing", "ongoing", "running":
		return StatusContinuing
	}
	return ""
}

// askRecord reads a series from a definition by an id Solarr holds (TheTVDB, IMDb, TMDB, in that order); a series with none is not asked.
func (s Service) askRecord(ctx context.Context, key string, ids IDs) (Series, bool) {
	provider := s.recordProvider(key)
	if provider == nil {
		return Series{}, false
	}
	for _, own := range []struct{ namespace, id string }{{"tvdb", idText(ids.TVDB)}, {"imdb", ids.IMDb}, {"tmdb", idText(ids.TMDB)}} {
		if own.id == "" {
			continue
		}
		record, err := provider.ByID(ctx, own.namespace, own.id)
		if err != nil {
			s.warn(key, ids.TVDB, err)
			continue
		}
		if record.Title != "" {
			return FromRecord(key, record), true
		}
	}
	return Series{}, false
}

func idText(id int64) string {
	if id <= 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
