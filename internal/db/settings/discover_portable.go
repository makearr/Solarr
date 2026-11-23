package settings

import (
	"context"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// DiscoverEntry is Discover's configuration in the export; libraries are keyed media_type/name because ids are not portable.
type DiscoverEntry struct {
	Enabled         bool                   `yaml:"enabled"`
	ExcludeGenres   []string               `yaml:"exclude_genres,omitempty"`
	ExcludeNetworks []string               `yaml:"exclude_networks,omitempty"`
	Libraries       []LibraryDiscoverEntry `yaml:"libraries,omitempty"`
}

// LibraryDiscoverEntry is one library's Discover settings.
type LibraryDiscoverEntry struct {
	Library           string                `yaml:"library"`
	Enabled           bool                  `yaml:"enabled"`
	TotalAmount       int64                 `yaml:"total_amount"`
	ClassicAgeYears   int64                 `yaml:"classic_age_years"`
	MinRating         float64               `yaml:"min_rating"`
	MinVotes          int64                 `yaml:"min_votes"`
	AutoAddPerDay     int64                 `yaml:"auto_add_per_day"`
	AutoAddSearch     bool                  `yaml:"auto_add_search,omitempty"`
	OriginalLanguages []string              `yaml:"original_languages,omitempty"`
	LanguagesSet      bool                  `yaml:"languages_set,omitempty"`
	ExcludeGenres     []string              `yaml:"exclude_genres,omitempty"`
	ExcludeNetworks   []string              `yaml:"exclude_networks,omitempty"`
	DisabledKinds     []string              `yaml:"disabled_kinds,omitempty"`
	Types             []DiscoverTypeSetting `yaml:"types,omitempty"`
}

func (e LibraryDiscoverEntry) setting(categoryID int64) LibraryDiscover {
	out := LibraryDiscover{
		CategoryID: categoryID, Enabled: e.Enabled, TotalAmount: e.TotalAmount, ClassicAgeYears: e.ClassicAgeYears, MinRating: e.MinRating,
		MinVotes: e.MinVotes, AutoAddPerDay: e.AutoAddPerDay, AutoAddSearch: e.AutoAddSearch, OriginalLanguages: e.OriginalLanguages,
		LanguagesSet: e.LanguagesSet, ExcludeGenres: e.ExcludeGenres, ExcludeNetworks: e.ExcludeNetworks, DisabledKinds: e.DisabledKinds,
	}
	out.Types = append(out.Types, e.Types...)
	return out
}

// ExportDiscover fills the document's Discover section from what is saved.
func (c *Cache) ExportDiscover(ctx context.Context, document *Document) error {
	libraries, err := c.LibraryDiscovers(ctx)
	if err != nil {
		return err
	}
	snapshot := c.Snapshot()
	global := snapshot.DiscoverGlobal()
	entry := &DiscoverEntry{Enabled: global.Enabled, ExcludeGenres: global.ExcludeGenres, ExcludeNetworks: global.ExcludeNetworks}
	for _, one := range libraries {
		category, found := snapshot.Category(one.CategoryID)
		if !found {
			continue
		}
		library := LibraryDiscoverEntry{
			Library: category.MediaType + "/" + category.Name, Enabled: one.Enabled, TotalAmount: one.TotalAmount, ClassicAgeYears: one.ClassicAgeYears,
			MinRating: one.MinRating, MinVotes: one.MinVotes, AutoAddPerDay: one.AutoAddPerDay, AutoAddSearch: one.AutoAddSearch,
			OriginalLanguages: one.OriginalLanguages, LanguagesSet: one.LanguagesSet, ExcludeGenres: one.ExcludeGenres,
			ExcludeNetworks: one.ExcludeNetworks, DisabledKinds: one.DisabledKinds,
		}
		library.Types = append(library.Types, one.Types...)
		entry.Libraries = append(entry.Libraries, library)
	}
	document.Discover = entry
	return nil
}

// validateDiscover checks every library's numbers; a library the instance lacks is skipped at import, not refused.
func (d Document) validateDiscover() config.Problems {
	var problems config.Problems
	if d.Discover == nil {
		return nil
	}
	for index, library := range d.Discover.Libraries {
		where := fmt.Sprintf("discover.libraries[%d]", index)
		for _, problem := range validateLibraryDiscover(library.setting(0)) {
			problem.Path = where + "." + problem.Path
			problems = append(problems, problem)
		}
	}
	return problems
}

// ImportDiscover applies the document's Discover section; libraries the instance does not have are skipped.
func (c *Cache) ImportDiscover(ctx context.Context, cl clock.Clock, document Document) error {
	if document.Discover == nil {
		return nil
	}
	snapshot := c.Snapshot()
	idByKey := map[string]int64{}
	for _, category := range snapshot.Categories() {
		idByKey[category.MediaType+"/"+category.Name] = category.ID
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := setDiscoverGlobal(ctx, queries, cl, DiscoverGlobal{
			Enabled: document.Discover.Enabled, ExcludeGenres: document.Discover.ExcludeGenres, ExcludeNetworks: document.Discover.ExcludeNetworks,
		}); err != nil {
			return err
		}
		for _, library := range document.Discover.Libraries {
			id, found := idByKey[library.Library]
			if !found {
				continue
			}
			if err := writeLibraryDiscover(ctx, queries, library.setting(id)); err != nil {
				return fmt.Errorf("discover library %q: %w", library.Library, err)
			}
		}
		return nil
	})
}
