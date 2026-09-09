package apiserver

import (
	"context"
	"net/http"
	"strconv"

	"github.com/makearr/MediaNexus/internal/apiserver/gen"
	"github.com/makearr/MediaNexus/internal/authz"
	"github.com/makearr/MediaNexus/internal/model"
	"github.com/makearr/MediaNexus/internal/settings"
)

// NavigationProvider resolves a media type's ADR-54 declared levels. Defined
// here and implemented by the caller (cmd/medianexus), the same reason
// WorksReader is: this package never imports internal/music directly.
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
			Levels: s.libraryLevels(r.Context(), category),
		}
	}
	writeJSON(w, s.logger, http.StatusOK, views)
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
// writes for it (internal/library/categorycounts.go). Organisation has no
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
