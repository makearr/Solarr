package tv

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"strconv"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/metadata/tmdb"
	"github.com/makearr/Solarr/internal/metadata/tvdb"
)

// FilmRemoteIDs is TheTVDB as the film step asks it: a film's ids at other sites, which name it in the library.
type FilmRemoteIDs interface {
	MovieRemoteIDs(ctx context.Context, id int64) (tvdb.Movie, error)
}

const (
	// filmsChecked is the source a series' lists are recorded as read under for their films, apart from listsChecked so
	// the step starts on series whose lists were read before it existed.
	filmsChecked = "tvdb-movies"
	// filmLookupsPerRun is the film lookups after which a pass starts no further series; the series begun is finished.
	filmLookupsPerRun = 120
	// listFilmsMax leaves a list of more films alone: it is a whole studio's catalogue, not one franchise.
	listFilmsMax = 40
)

// listReads keeps what one pass read of TheTVDB's lists, so the crossover and film steps ask each once.
type listReads struct {
	source  SeriesLists
	lists   map[int64][]tvdb.List
	entries map[int64][]tvdb.ListEntry
}

func newListReads(source SeriesLists) *listReads {
	return &listReads{source: source, lists: map[int64][]tvdb.List{}, entries: map[int64][]tvdb.ListEntry{}}
}

func (r *listReads) seriesLists(ctx context.Context, id int64) ([]tvdb.List, error) {
	if lists, ok := r.lists[id]; ok {
		return lists, nil
	}
	lists, err := r.source.SeriesLists(ctx, id)
	if err == nil {
		r.lists[id] = lists
	}
	return lists, err
}

func (r *listReads) listEntries(ctx context.Context, id int64) ([]tvdb.ListEntry, error) {
	if entries, ok := r.entries[id]; ok {
		return entries, nil
	}
	entries, err := r.source.ListEntries(ctx, id)
	if err == nil {
		r.entries[id] = entries
	}
	return entries, err
}

// franchiseListValue is the classification a title on one of TheTVDB's official lists carries.
func franchiseListValue(listID int64) string {
	return ProviderTVDB + ":" + strconv.FormatInt(listID, 10)
}

// filmLookups resolves TheTVDB's film ids to the library's films for one pass, counting the lookups; 0 is a film the library lacks.
type filmLookups struct {
	pass     FranchisePass
	resolved map[int64]int64
	left     int
}

// recordListFilms marks every held series and film of each official list with films the due series are on, so franchiselink
// links the collections mostly on one list (documentation/metadata-providers.md); a person's unlink stays, as for every provider link.
func (p FranchisePass) recordListFilms(ctx context.Context, reads *listReads) error {
	if p.Lists == nil || p.Films == nil || p.FilmType == "" {
		return nil
	}
	due, err := p.Franchises.WorksToCheck(ctx, MediaType, ProviderTVDB, filmsChecked, crossoverRecheck, 0, listSeriesPerRun)
	if err != nil {
		return err
	}
	lookups := &filmLookups{pass: p, resolved: map[int64]int64{}, left: filmLookupsPerRun}
	written := map[int64]bool{}
	var answered []int64
	for _, work := range due {
		id, err := strconv.ParseInt(work.ExternalID, 10, 64)
		if err != nil || id <= 0 {
			continue
		}
		if lookups.left <= 0 {
			break
		}
		lists, err := reads.seriesLists(ctx, id)
		if err != nil {
			p.Logger.Debug("series lists not read", slog.Int64("tvdb", id), slog.Any("error", err))
			continue
		}
		complete := true
		var onLists []string
		for _, list := range lists {
			if !list.IsOfficial {
				continue
			}
			entries, err := reads.listEntries(ctx, list.ID)
			if err != nil {
				complete = false
				continue
			}
			films := listFilms(entries)
			if len(films) == 0 || len(films) > listFilmsMax {
				continue
			}
			onLists = append(onLists, franchiseListValue(list.ID))
			if written[list.ID] {
				continue
			}
			members, ok, err := lookups.members(ctx, entries, films)
			if err != nil {
				return err
			}
			if !ok {
				complete = false
				continue
			}
			if err := p.Franchises.SetClassificationMembers(ctx, library.FranchiseListKind, franchiseListValue(list.ID), ProviderTVDB, members); err != nil {
				return err
			}
			written[list.ID] = true
		}
		if !complete {
			continue
		}
		if err := p.Franchises.KeepClassifications(ctx, work.WorkID, library.FranchiseListKind, onLists); err != nil {
			return err
		}
		answered = append(answered, work.WorkID)
	}
	if err := p.Franchises.MarkChecked(ctx, filmsChecked, answered); err != nil {
		return err
	}
	p.Logger.Info("franchise list films recorded", slog.Int("lists", len(written)), slog.Int("film lookups", filmLookupsPerRun-lookups.left))
	return nil
}

// listFilms are the distinct film ids of a list.
func listFilms(entries []tvdb.ListEntry) []int64 {
	var films []int64
	for _, entry := range entries {
		if entry.MovieID > 0 && !slices.Contains(films, entry.MovieID) {
			films = append(films, entry.MovieID)
		}
	}
	return films
}

// members are the library's series and films a list names; false when a film's ids could not be read, so the list is
// not half written.
func (l *filmLookups) members(ctx context.Context, entries []tvdb.ListEntry, films []int64) ([]int64, bool, error) {
	var members []int64
	add := func(workID int64) {
		if workID > 0 && !slices.Contains(members, workID) {
			members = append(members, workID)
		}
	}
	for _, entry := range entries {
		if entry.SeriesID <= 0 {
			continue
		}
		workID, found, err := l.pass.Series.WorkByProviderID(ctx, MediaType, ProviderTVDB, strconv.FormatInt(entry.SeriesID, 10))
		if err != nil {
			return nil, false, err
		}
		if found {
			add(workID)
		}
	}
	for _, film := range films {
		workID, ok, err := l.film(ctx, film)
		if err != nil || !ok {
			return nil, false, err
		}
		add(workID)
	}
	slices.Sort(members)
	return members, true, nil
}

// film is the library's film TheTVDB's id names, by its TMDB id else its IMDb one; false when TheTVDB did not answer.
func (l *filmLookups) film(ctx context.Context, id int64) (int64, bool, error) {
	if workID, ok := l.resolved[id]; ok {
		return workID, true, nil
	}
	l.left--
	movie, err := l.pass.Films.MovieRemoteIDs(ctx, id)
	if errors.Is(err, tvdb.ErrNotFound) {
		l.resolved[id] = 0
		return 0, true, nil
	}
	if err != nil {
		l.pass.Logger.Debug("film ids not read", slog.Int64("tvdb", id), slog.Any("error", err))
		return 0, false, nil
	}
	var workID int64
	for _, remote := range []struct{ site, provider string }{{"TheMovieDB.com", tmdb.KeyMovie}, {"IMDB", ProviderIMDb}} {
		externalID := movie.RemoteID(remote.site)
		if externalID == "" {
			continue
		}
		found, ok, err := l.pass.Series.WorkByProviderID(ctx, l.pass.FilmType, remote.provider, externalID)
		if err != nil {
			return 0, false, err
		}
		if ok {
			workID = found
			break
		}
	}
	l.resolved[id] = workID
	return workID, true, nil
}
