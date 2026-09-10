package apiserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/MediaNexus/internal/api/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/api/authz"
	"github.com/makearr/MediaNexus/internal/db/settings"
	"github.com/makearr/MediaNexus/internal/identify/model"
	"github.com/makearr/MediaNexus/internal/platform/paginate"
)

// NavigationProvider resolves a media type's ADR-54 declared levels. Defined
// here and implemented by the caller (cmd/medianexus), the same reason
// WorksReader is: this package never imports internal/media/music directly.
type NavigationProvider interface {
	LevelsFor(mediaType string) ([]model.Level, bool)
}

// ListLibraries is GET /libraries, the navigation tree's root: every
// category the caller may read (ADR-27), with the entity levels its media
// type declares and each level's count. A media type with no registered
// NavigationProvider entry answers no levels rather than failing the whole
// list -- the same "render without the tile" fallback categoryCounts uses.
func (s *Server) ListLibraries(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	visible := access.Categories(settings.PermissionRead)
	views := make([]gen.Library, len(visible))
	for index, category := range visible {
		views[index] = gen.Library{
			Id: category.ID, MediaType: category.MediaType, Name: category.Name,
			Levels:    s.libraryLevels(r.Context(), category),
			LastLevel: s.lastLibraryLevel(r.Context(), access.UserID(), category.ID),
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// lastLevelPreferenceKey is where ADR-54's last-visited level is stored,
// one row per (user, library) in settings.db's user_preference table.
func lastLevelPreferenceKey(categoryID int64) string {
	return "library.last_level." + strconv.FormatInt(categoryID, 10)
}

// lastLibraryLevel reads a caller's own last-visited level for one library,
// nil if never visited or if preferences are unavailable -- the same
// "render without the tile" fallback as every other optional figure here.
func (s *Server) lastLibraryLevel(ctx context.Context, userID, categoryID int64) *string {
	if s.auth.Settings == nil {
		return nil
	}
	raw, ok, err := s.auth.Settings.GetPreference(ctx, userID, lastLevelPreferenceKey(categoryID))
	if err != nil {
		s.logger.WarnContext(ctx, "last library level unavailable",
			"user_id", userID, "category_id", categoryID, "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	var level string
	if err := json.Unmarshal([]byte(raw), &level); err != nil {
		return nil
	}
	return &level
}

// routeSetLibraryLastLevel parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeSetLibraryLastLevel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a library id must be a number")
		return
	}
	s.SetLibraryLastLevel(w, r, id)
}

// SetLibraryLastLevel stores the level the caller last visited in one
// library (ADR-54), so the sidebar tree costs one click on return.
func (s *Server) SetLibraryLastLevel(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(id, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	var body gen.SetLibraryLastLevelJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}
	if _, ok := s.findLevel(category.MediaType, body.Level); !ok {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
			"Not found", "this library declares no such level")
		return
	}

	encoded, err := json.Marshal(body.Level)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.auth.Settings == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "preferences are not available")
		return
	}
	if err := s.auth.Settings.SetPreference(r.Context(), access.UserID(), lastLevelPreferenceKey(id), string(encoded)); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// libraryLevels renders one category's declared levels with their counts.
// Never errors: an unavailable counter or an unregistered media type both
// degrade to an absent/empty answer rather than failing navigation entirely.
func (s *Server) libraryLevels(ctx context.Context, category settings.Category) []gen.LibraryLevel {
	if s.navigation == nil {
		return []gen.LibraryLevel{}
	}
	levels, ok := s.navigation.LevelsFor(category.MediaType)
	if !ok {
		return []gen.LibraryLevel{}
	}

	values := s.counterValues(ctx, category.ID)
	views := make([]gen.LibraryLevel, len(levels))
	for index, level := range levels {
		views[index] = gen.LibraryLevel{
			Key: level.Key, Label: level.Label, DefaultSort: level.DefaultSort,
			Entity: gen.LibraryLevelEntity(level.Entity),
			Count:  countFor(values, level.Entity),
		}
	}
	return views
}

// counterValues reads every metric for one category's scope, once, so
// libraryLevels does not round-trip per level. Unavailable answers an empty
// map, the same fallback categoryCounts already uses for a single metric.
func (s *Server) counterValues(ctx context.Context, categoryID int64) map[string]int64 {
	if s.librarySize == nil {
		return nil
	}
	values, err := s.librarySize.Counters(ctx, "category:"+strconv.FormatInt(categoryID, 10))
	if err != nil {
		s.logger.WarnContext(ctx, "library level counters unavailable",
			"category_id", categoryID, "error", err)
		return nil
	}
	return values
}

// countFor maps a level's entity to the counter metric CategoryCounters
// writes for it (internal/db/library/categorycounts.go). Organisation has no
// counter yet -- no media type declares an organisation-entity level today.
func countFor(values map[string]int64, entity model.LevelEntity) *int {
	var metric string
	switch entity {
	case model.LevelEntityWork:
		metric = "works"
	case model.LevelEntityWorkPart:
		metric = "parts"
	case model.LevelEntityPerson:
		metric = "artists"
	default:
		return nil
	}
	value, ok := values[metric]
	if !ok {
		return nil
	}
	count := int(value)
	return &count
}

// TracksFilter narrows ListLibraryTracks -- ADR-27's two visibility halves,
// exactly WorksFilter's own fields.
type TracksFilter struct {
	RootFolderIDs []int64
	CategoryIDs   []int64
}

// TracksCursor is a position among the albums backing a Tracks page --
// library.TrackAlbumCursor's own shape, since an album's parts are never
// split across two pages.
type TracksCursor struct {
	ArtistSortName string
	AlbumSortTitle string
	AlbumID        int64
}

// TrackRow is one work_part with its parent album's display fields.
type TrackRow struct {
	ID         int64
	Title      *string
	AlbumTitle string
	AlbumYear  *int64
	ArtistName *string
}

// LibraryTracksReader lists the Tracks navigation level. Defined here and
// implemented by the caller, so this package never imports internal/db/library
// directly (WorksReader's own doc comment explains why).
type LibraryTracksReader interface {
	ListLibraryTracks(ctx context.Context, filter TracksFilter, cursor TracksCursor, albumPageSize int) (
		rows []TrackRow, nextCursor TracksCursor, hasMore bool, err error)
}

// ArtistsFilter narrows ListLibraryArtists -- ADR-27's two visibility
// halves, exactly WorksFilter's own fields.
type ArtistsFilter struct {
	RootFolderIDs []int64
	CategoryIDs   []int64
}

// ArtistsCursor is a position in the Artists level, ordered by sort name.
type ArtistsCursor struct {
	SortName string
	ID       int64
}

// ArtistRow is one person credited as an artist on a work visible to the
// library (ADR-54's Artists level).
type ArtistRow struct {
	ID       int64
	Name     string
	SortName string
}

// LibraryArtistsReader lists the Artists navigation level (a person-entity
// level). Defined here and implemented by the caller, so this package never
// imports internal/db/library directly (WorksReader's own doc comment explains
// why) -- separate from PeopleReader, which is deliberately not
// category-scoped (a person spans several media types at once, ADR-32).
type LibraryArtistsReader interface {
	ListLibraryArtists(ctx context.Context, filter ArtistsFilter, cursor ArtistsCursor, pageSize int) ([]ArtistRow, error)
}

// levelCursor is GET /libraries/{id}/{level}'s own opaque cursor: a handful
// of text fields plus an id tie-break, general enough for any level's own
// sort key (Albums needs one field, Tracks needs two) without a new codec
// per level -- the same reasoning LibraryLevelItem's own genericity has.
type levelCursor struct {
	fields []string
	id     int64
}

const levelCursorFieldSep = "\x1f"

func (c levelCursor) encode() string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(c.id, 10) + ":" + strings.Join(c.fields, levelCursorFieldSep)))
}

func decodeLevelCursor(raw string) (levelCursor, error) {
	if raw == "" {
		return levelCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return levelCursor{}, fmt.Errorf("%w: not base64", paginate.ErrBadCursor)
	}
	idPart, rest, found := strings.Cut(string(decoded), ":")
	if !found {
		return levelCursor{}, fmt.Errorf("%w: malformed", paginate.ErrBadCursor)
	}
	id, err := strconv.ParseInt(idPart, 10, 64)
	if err != nil {
		return levelCursor{}, fmt.Errorf("%w: id is not a number", paginate.ErrBadCursor)
	}
	return levelCursor{fields: strings.Split(rest, levelCursorFieldSep), id: id}, nil
}

// field returns fields[index], or "" past the end -- a fresh cursor decodes
// to one empty field, not len(fields)==N for whatever N a level needs.
func (c levelCursor) field(index int) string {
	if index >= len(c.fields) {
		return ""
	}
	return c.fields[index]
}

// routeListLibraryLevel parses the id/level path parameters and the cursor/
// limit query parameters by hand, mirroring routeListWorks -- this project
// registers routes directly rather than through the generated
// ServerInterfaceWrapper, for full control over the middleware chain.
func (s *Server) routeListLibraryLevel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a library id must be a number")
		return
	}
	level := chi.URLParam(r, "level")

	query := r.URL.Query()
	var params gen.ListLibraryLevelParams
	if raw := query.Get("cursor"); raw != "" {
		params.Cursor = &raw
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
				"Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListLibraryLevel(w, r, id, level, params)
}

// ListLibraryLevel answers one page of one navigation level (ADR-54). The
// response is one generic row shape across every entity a level can back --
// only one media type exists today, so there is no second real case yet to
// generalize a typed-per-level response from (the spec's own description has
// the detail).
func (s *Server) ListLibraryLevel(w http.ResponseWriter, r *http.Request, id int64, level string, params gen.ListLibraryLevelParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(id, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	found, ok := s.findLevel(category.MediaType, level)
	if !ok {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound,
			"Not found", "this library declares no such level")
		return
	}

	cursor, err := decodeLevelCursor(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	rootFolderIDs, categoryIDs, err := s.visibility(access, &id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	switch found.Entity {
	case model.LevelEntityWork:
		s.listLibraryLevelWorks(w, r, rootFolderIDs, categoryIDs, cursor, limit)
	case model.LevelEntityWorkPart:
		s.listLibraryLevelTracks(w, r, rootFolderIDs, categoryIDs, cursor, limit)
	case model.LevelEntityPerson:
		s.listLibraryLevelArtists(w, r, rootFolderIDs, categoryIDs, cursor, limit)
	default:
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "this level has no reader wired up yet")
	}
}

// findLevel looks up one level by key among what mediaType declares.
func (s *Server) findLevel(mediaType, key string) (model.Level, bool) {
	if s.navigation == nil {
		return model.Level{}, false
	}
	levels, ok := s.navigation.LevelsFor(mediaType)
	if !ok {
		return model.Level{}, false
	}
	for _, level := range levels {
		if level.Key == key {
			return level, true
		}
	}
	return model.Level{}, false
}

// listLibraryLevelWorks serves a work-entity level (Albums) over the
// existing WorksReader -- a level is a filter and a default sort, not a new
// list implementation.
func (s *Server) listLibraryLevelWorks(
	w http.ResponseWriter, r *http.Request, rootFolderIDs, categoryIDs []int64, cursor levelCursor, limit int,
) {
	if s.works == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}
	rows, err := s.works.ListWorks(r.Context(),
		WorksFilter{RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs},
		WorksCursor{SortTitle: cursor.field(0), ID: cursor.id}, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	items := rows
	var next string
	if len(rows) > limit {
		items = rows[:limit]
		last := items[len(items)-1]
		next = (levelCursor{fields: []string{last.SortTitle}, id: last.ID}).encode()
	}

	views := make([]gen.LibraryLevelItem, len(items))
	for index, row := range items {
		views[index] = gen.LibraryLevelItem{
			Id: row.ID, Title: row.Title, Subtitle: row.Artist, Year: yearOf(row.Year),
		}
	}
	writeLibraryLevelPage(w, s.logger, views, next)
}

// listLibraryLevelTracks serves a work_part-entity level (Tracks) over
// LibraryTracksReader.
func (s *Server) listLibraryLevelTracks(
	w http.ResponseWriter, r *http.Request, rootFolderIDs, categoryIDs []int64, cursor levelCursor, limit int,
) {
	if s.libraryTracks == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}
	rows, next, hasMore, err := s.libraryTracks.ListLibraryTracks(r.Context(),
		TracksFilter{RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs},
		TracksCursor{ArtistSortName: cursor.field(0), AlbumSortTitle: cursor.field(1), AlbumID: cursor.id}, limit)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	var nextEncoded string
	if hasMore {
		nextEncoded = (levelCursor{
			fields: []string{next.ArtistSortName, next.AlbumSortTitle}, id: next.AlbumID,
		}).encode()
	}

	views := make([]gen.LibraryLevelItem, len(rows))
	for index, row := range rows {
		views[index] = gen.LibraryLevelItem{
			Id: row.ID, Title: derefOr(row.Title, ""), Subtitle: trackSubtitle(row), Year: yearOf(row.AlbumYear),
		}
	}
	writeLibraryLevelPage(w, s.logger, views, nextEncoded)
}

// listLibraryLevelArtists serves a person-entity level (Artists) over
// LibraryArtistsReader.
func (s *Server) listLibraryLevelArtists(
	w http.ResponseWriter, r *http.Request, rootFolderIDs, categoryIDs []int64, cursor levelCursor, limit int,
) {
	if s.libraryArtists == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}
	rows, err := s.libraryArtists.ListLibraryArtists(r.Context(),
		ArtistsFilter{RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs},
		ArtistsCursor{SortName: cursor.field(0), ID: cursor.id}, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	items := rows
	var next string
	if len(rows) > limit {
		items = rows[:limit]
		last := items[len(items)-1]
		next = (levelCursor{fields: []string{last.SortName}, id: last.ID}).encode()
	}

	views := make([]gen.LibraryLevelItem, len(items))
	for index, row := range items {
		views[index] = gen.LibraryLevelItem{Id: row.ID, Title: row.Name}
	}
	writeLibraryLevelPage(w, s.logger, views, next)
}

// trackSubtitle is a track row's secondary line: its artist and album,
// whichever of the two are actually known.
func trackSubtitle(row TrackRow) *string {
	artist := derefOr(row.ArtistName, "")
	if artist == "" {
		return &row.AlbumTitle
	}
	subtitle := artist + " -- " + row.AlbumTitle
	return &subtitle
}

// writeLibraryLevelPage renders the shared LibraryLevelPage shape every
// level's dispatch function ends with.
func writeLibraryLevelPage(w http.ResponseWriter, logger *slog.Logger, items []gen.LibraryLevelItem, nextCursor string) {
	page := gen.LibraryLevelPage{Items: items}
	if nextCursor != "" {
		page.NextCursor = &nextCursor
	}
	writeJSON(w, logger, http.StatusOK, page)
}
