package library

import (
	"context"
	"strings"
	"unicode"
)

// LinkReasonShow says a film collection is linked with a series because the films are the show's: the names say so.
const LinkReasonShow = "show"

// NamedCollection is a film collection as the show rule sees it.
type NamedCollection struct {
	ID   int64
	Name string
}

// NamedWork is a film or a series by title.
type NamedWork struct {
	WorkID int64
	Title  string
}

// PlannedShowLink says the films of a collection (or one loose film) are the movies of a series.
type PlannedShowLink struct {
	// FilmCollectionID is the collection that holds the films; zero when FilmWorkID is a loose film that needs one.
	FilmCollectionID int64
	FilmWorkID       int64
	FilmName         string
	SeriesWorkID     int64
	SeriesTitle      string
}

// collectionSuffixes end the name of a film collection that is named after what it collects.
var collectionSuffixes = []string{
	"film collection", "movie collection", "film series", "movie series", "collection", "saga", "franchise", "trilogy",
	"anthology", "films", "movies",
}

// movieSuffixes end the title of a film made from a show.
var movieSuffixes = []string{"the motion picture", "the movie", "the film", "movie", "film"}

// showStem reduces a title to what to compare: lower case, letters and digits, no leading article.
func showStem(title string) string {
	var builder strings.Builder
	space := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && builder.Len() > 0 {
				builder.WriteByte(' ')
			}
			space = false
			builder.WriteRune(r)
		default:
			space = true
		}
	}
	stem := builder.String()
	for _, article := range []string{"the ", "a ", "an "} {
		stem = strings.TrimPrefix(stem, article)
	}
	return stem
}

// withoutSuffix drops the first of the suffixes the stem ends with, as whole words.
func withoutSuffix(stem string, suffixes []string) string {
	for _, suffix := range suffixes {
		if trimmed, found := strings.CutSuffix(stem, " "+suffix); found {
			return trimmed
		}
	}
	return stem
}

// PlanShowFilmLinks finds the film collections, and the loose films, that are a series' movies: a collection named for
// the show ("Sex and the City Collection"), or a film titled as the show's movie ("Veronica Mars: The Movie").
// Several series of one name match none: a reboot cannot be told from the original by its title.
func PlanShowFilmLinks(collections []NamedCollection, looseFilms, series []NamedWork) []PlannedShowLink {
	byStem := map[string][]NamedWork{}
	for _, show := range series {
		if stem := showStem(show.Title); stem != "" {
			byStem[stem] = append(byStem[stem], show)
		}
	}
	only := func(stem string) (NamedWork, bool) {
		shows := byStem[stem]
		if len(shows) != 1 {
			return NamedWork{}, false
		}
		return shows[0], true
	}
	var planned []PlannedShowLink
	for _, collection := range collections {
		stem := withoutSuffix(showStem(collection.Name), collectionSuffixes)
		if show, found := only(stem); stem != "" && found {
			planned = append(planned, PlannedShowLink{FilmCollectionID: collection.ID, FilmName: collection.Name, SeriesWorkID: show.WorkID, SeriesTitle: show.Title})
		}
	}
	for _, film := range looseFilms {
		whole := showStem(film.Title)
		stem := withoutSuffix(whole, movieSuffixes)
		if stem == whole || stem == "" {
			continue
		}
		if show, found := only(stem); found {
			planned = append(planned, PlannedShowLink{FilmWorkID: film.WorkID, FilmName: show.Title, SeriesWorkID: show.WorkID, SeriesTitle: show.Title})
		}
	}
	return planned
}

// ShowFilmInputs are the films and series the show rule reads, and the collection each series is in.
type ShowFilmInputs struct {
	FilmCollections []NamedCollection
	LooseFilms      []NamedWork
	Series          []NamedWork
	// SeriesCollection is the collection of the series library a series already belongs to, if any.
	SeriesCollection map[int64]int64
}

// ShowFilmInputs reads them: film collections (not ones made by hand), the films that are in no collection, every
// series of the "tv" type, and the series collection each is in.
func (f Franchises) ShowFilmInputs(ctx context.Context, filmType, seriesType string) (ShowFilmInputs, error) {
	var inputs ShowFilmInputs
	summaries, err := f.Summaries(ctx, filmType)
	if err != nil {
		return inputs, err
	}
	for _, summary := range summaries {
		if summary.Source != "custom" {
			inputs.FilmCollections = append(inputs.FilmCollections, NamedCollection{ID: summary.ID, Name: summary.Name})
		}
	}
	filmMemberships, err := f.FranchiseMemberships(ctx, filmType)
	if err != nil {
		return inputs, err
	}
	collected := map[int64]bool{}
	for _, membership := range filmMemberships {
		collected[membership.WorkID] = true
	}
	films, err := f.WorkNames(ctx, filmType)
	if err != nil {
		return inputs, err
	}
	for _, film := range films {
		if !collected[film.WorkID] {
			inputs.LooseFilms = append(inputs.LooseFilms, NamedWork{WorkID: film.WorkID, Title: film.Title})
		}
	}
	shows, err := f.WorkNames(ctx, seriesType)
	if err != nil {
		return inputs, err
	}
	for _, show := range shows {
		inputs.Series = append(inputs.Series, NamedWork{WorkID: show.WorkID, Title: show.Title})
	}
	seriesMemberships, err := f.FranchiseMemberships(ctx, seriesType)
	if err != nil {
		return inputs, err
	}
	inputs.SeriesCollection = map[int64]int64{}
	for _, membership := range seriesMemberships {
		if current, found := inputs.SeriesCollection[membership.WorkID]; !found || membership.CollectionID < current {
			inputs.SeriesCollection[membership.WorkID] = membership.CollectionID
		}
	}
	return inputs, nil
}

// MakeShowLink links the films' collection with the series' collection, making either that is missing: a collection of
// the film (a loose one) or of the show alone, named for the series. It answers whether a link was made.
func (f Franchises) MakeShowLink(ctx context.Context, plan PlannedShowLink, filmType, seriesType string, seriesCollectionID int64) (bool, error) {
	filmCollection := plan.FilmCollectionID
	if filmCollection == 0 {
		id, err := f.CreateAuto(ctx, filmType, plan.FilmName)
		if err != nil {
			return false, err
		}
		if err := f.AddToFranchise(ctx, id, []int64{plan.FilmWorkID}); err != nil {
			return false, err
		}
		filmCollection = id
	}
	if seriesCollectionID == 0 {
		id, err := f.CreateAuto(ctx, seriesType, plan.SeriesTitle)
		if err != nil {
			return false, err
		}
		if err := f.AddToFranchise(ctx, id, []int64{plan.SeriesWorkID}); err != nil {
			return false, err
		}
		seriesCollectionID = id
	}
	return f.AutoLink(ctx, PlannedLink{A: filmCollection, B: seriesCollectionID, Reason: LinkReasonShow, Detail: plan.SeriesTitle})
}
