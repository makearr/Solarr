package importlist

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/metadata/anilist"
)

// maxSeasonPages bounds a season's read: 150 entries, past which a season holds little anyone adds.
const maxSeasonPages = 3

// The ids an AniList entry carries, as works store them.
const (
	providerAniList = "anilist"
	providerMAL     = "mal"
)

// AniListLists is the AniList client as its lists need it.
type AniListLists interface {
	Season(ctx context.Context, season string, year, page int) ([]anilist.Media, bool, error)
	UserList(ctx context.Context, userName string) ([]anilist.ListedMedia, error)
}

// Season is one of AniList's seasons of one year.
type Season struct {
	Name string
	Year int
}

// SeasonOf is the season a moment falls in: winter is January to March, as AniList counts.
func SeasonOf(at time.Time) Season {
	names := []string{anilist.SeasonWinter, anilist.SeasonSpring, anilist.SeasonSummer, anilist.SeasonFall}
	return Season{Name: names[(int(at.Month())-1)/3], Year: at.Year()}
}

// Next is the season after this one.
func (s Season) Next() Season {
	names := []string{anilist.SeasonWinter, anilist.SeasonSpring, anilist.SeasonSummer, anilist.SeasonFall}
	index := slices.Index(names, s.Name)
	if index == len(names)-1 {
		return Season{Name: names[0], Year: s.Year + 1}
	}
	return Season{Name: names[index+1], Year: s.Year}
}

// The setting keys of AniList's lists.
const (
	seasonField   = "season"
	yearField     = "year"
	userField     = "username"
	statusesField = "statuses"
)

// The relative seasons a season list may follow instead of a fixed one.
const (
	seasonCurrent = "current"
	seasonNext    = "next"
)

var seasonFields = []Field{
	{Key: seasonField, Label: "Season", Kind: FieldChoice, Default: seasonCurrent, Choices: []media.Choice{
		{Key: seasonCurrent, Label: "The current season"}, {Key: seasonNext, Label: "The next season"},
		{Key: anilist.SeasonWinter, Label: "Winter"}, {Key: anilist.SeasonSpring, Label: "Spring"},
		{Key: anilist.SeasonSummer, Label: "Summer"}, {Key: anilist.SeasonFall, Label: "Fall"},
	}},
	{Key: yearField, Label: "Year", Kind: FieldYear, Help: "For a named season; empty is this year."},
}

var userFields = []Field{
	{Key: userField, Label: "AniList user name", Kind: FieldText, Required: true},
	{Key: statusesField, Label: "Statuses", Kind: FieldChoices, Default: "CURRENT,PLANNING", Choices: []media.Choice{
		{Key: "CURRENT", Label: "Watching"}, {Key: "PLANNING", Label: "Planning"}, {Key: "COMPLETED", Label: "Completed"},
		{Key: "PAUSED", Label: "Paused"}, {Key: "DROPPED", Label: "Dropped"}, {Key: "REPEATING", Label: "Rewatching"},
	}},
}

// AniListSeason is the anime of one season.
type AniListSeason struct {
	Client AniListLists
	// Now is the season it is, for a list following the current or next one.
	Now func() Season
}

// Entries implements Source.
func (s AniListSeason) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	season := s.Now()
	switch picked := option(options, seasonFields, seasonField); picked {
	case seasonCurrent:
	case seasonNext:
		season = season.Next()
	default:
		season.Name = picked
		if year, err := strconv.Atoi(option(options, seasonFields, yearField)); err == nil {
			season.Year = year
		}
	}
	var entries []media.ListEntry
	for page := 1; page <= maxSeasonPages; page++ {
		found, more, err := s.Client.Season(ctx, season.Name, season.Year, page)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
		}
		for _, entry := range found {
			entries = append(entries, aniListEntry(entry))
		}
		if !more {
			break
		}
	}
	return entries, nil
}

// AniListUser is what one user has on their AniList list, in the statuses chosen.
type AniListUser struct{ Client AniListLists }

// Entries implements Source.
func (u AniListUser) Entries(ctx context.Context, options Options) ([]media.ListEntry, error) {
	listed, err := u.Client.UserList(ctx, strings.TrimSpace(options[userField]))
	if errors.Is(err, anilist.ErrUnknownUser) {
		return nil, fmt.Errorf("%w: %w", ErrListNotFound, err)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSourceUnavailable, err)
	}
	statuses := strings.Split(option(options, userFields, statusesField), ",")
	var entries []media.ListEntry
	for _, entry := range listed {
		if slices.Contains(statuses, entry.Status) {
			entries = append(entries, aniListEntry(entry.Media))
		}
	}
	return entries, nil
}

func aniListEntry(entry anilist.Media) media.ListEntry {
	title := entry.Title.English
	if title == "" {
		title = entry.Title.Romaji
	}
	ids := []media.ProviderID{{Provider: providerAniList, ExternalID: strconv.FormatInt(entry.ID, 10)}}
	if entry.IDMal > 0 {
		ids = append(ids, media.ProviderID{Provider: providerMAL, ExternalID: strconv.FormatInt(entry.IDMal, 10)})
	}
	return media.ListEntry{
		Title: title, Year: entry.SeasonYear, Form: entry.Format, IDs: ids, AltTitles: entry.Titles(),
		PosterURL: entry.CoverImage.ExtraLarge,
	}
}
