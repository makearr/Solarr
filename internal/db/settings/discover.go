package settings

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// The instance-wide Discover switch and its exclusion lists, both on Settings > General and Settings > Discover.
const (
	KeyDiscoverEnabled         = "discover.enabled"
	KeyDiscoverExcludeGenres   = "discover.exclude_genres"
	KeyDiscoverExcludeNetworks = "discover.exclude_networks"
)

// DiscoverTypes are the recommendation lists a library can enable, in the order the interface shows them.
var DiscoverTypes = []string{"highest_rated", "trending", "upcoming", "classics", "collections"}

// What a library starts with until its Discover settings are saved.
const (
	DefaultDiscoverTotal         = 60
	DefaultDiscoverClassicYears  = 25
	DefaultDiscoverMinRating     = 7.0
	DefaultDiscoverMinVotes      = 1000
	DefaultDiscoverAutoAddPerDay = 5
	DefaultDiscoverTypeAmount    = 20
)

// DiscoverGlobal is what applies to every library.
type DiscoverGlobal struct {
	Enabled         bool
	ExcludeGenres   []string
	ExcludeNetworks []string
}

// DiscoverTypeSetting is one recommendation list of one library.
type DiscoverTypeSetting struct {
	Type    string `yaml:"type"`
	Enabled bool   `yaml:"enabled"`
	Amount  int64  `yaml:"amount"`
	AutoAdd bool   `yaml:"auto_add,omitempty"`
}

// LibraryDiscover is one library's Discover settings, defaults resolved.
type LibraryDiscover struct {
	CategoryID      int64
	Name            string
	MediaType       string
	Enabled         bool
	TotalAmount     int64
	ClassicAgeYears int64
	MinRating       float64
	MinVotes        int64
	AutoAddPerDay   int64
	AutoAddSearch   bool
	// OriginalLanguages is only meaningful when LanguagesSet; otherwise the caller prefills from the language profile.
	OriginalLanguages []string
	LanguagesSet      bool
	ExcludeGenres     []string
	ExcludeNetworks   []string
	// DisabledKinds names add kinds (an album, a performer) the library does not recommend.
	DisabledKinds []string
	// Types always holds every entry of DiscoverTypes, in that order.
	Types []DiscoverTypeSetting
}

// DiscoverGlobal answers the switch and the exclusion lists that apply to every library.
func (s *Snapshot) DiscoverGlobal() DiscoverGlobal {
	return DiscoverGlobal{
		Enabled:         s.policyBool(KeyDiscoverEnabled, false),
		ExcludeGenres:   s.policyStringList(KeyDiscoverExcludeGenres),
		ExcludeNetworks: s.policyStringList(KeyDiscoverExcludeNetworks),
	}
}

// discoverEnabledAllRepair records that the first global switch-on already enabled every library.
const discoverEnabledAllRepair = "discover_enable_all_libraries"

// SetDiscoverEnabled switches Discover on or off for the instance; the first switch-on enables every library.
func (c *Cache) SetDiscoverEnabled(ctx context.Context, cl clock.Clock, enabled bool) error {
	libraries, err := c.librariesToEnableOnce(ctx, enabled)
	if err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := setInstanceSettings(ctx, queries, cl, map[string]string{KeyDiscoverEnabled: boolPolicy(enabled)}); err != nil {
			return err
		}
		return enableLibrariesOnce(ctx, queries, cl, libraries)
	})
}

// librariesToEnableOnce answers every library with Discover enabled when this is the first switch-on, otherwise nil.
func (c *Cache) librariesToEnableOnce(ctx context.Context, enabled bool) ([]LibraryDiscover, error) {
	if !enabled || c.Snapshot().OneOffDone(discoverEnabledAllRepair) {
		return nil, nil
	}
	libraries, err := c.LibraryDiscovers(ctx)
	if err != nil {
		return nil, err
	}
	for i := range libraries {
		libraries[i].Enabled = true
	}
	return libraries, nil
}

// enableLibrariesOnce saves the libraries enabled and records the one-time enable; nil libraries (not the first switch-on) do nothing.
func enableLibrariesOnce(ctx context.Context, queries *settingsdb.Queries, cl clock.Clock, libraries []LibraryDiscover) error {
	if libraries == nil {
		return nil
	}
	for _, one := range libraries {
		if err := writeLibraryDiscover(ctx, queries, one); err != nil {
			return err
		}
	}
	return setInstanceSettings(ctx, queries, cl, map[string]string{oneOffPrefix + discoverEnabledAllRepair: "true"})
}

// SetDiscoverGlobal writes the switch and both exclusion lists together; the first switch-on enables every library.
func (c *Cache) SetDiscoverGlobal(ctx context.Context, cl clock.Clock, global DiscoverGlobal) error {
	libraries, err := c.librariesToEnableOnce(ctx, global.Enabled)
	if err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := setDiscoverGlobal(ctx, queries, cl, global); err != nil {
			return err
		}
		return enableLibrariesOnce(ctx, queries, cl, libraries)
	})
}

func setDiscoverGlobal(ctx context.Context, queries *settingsdb.Queries, cl clock.Clock, global DiscoverGlobal) error {
	genres, err := json.Marshal(cleanedList(global.ExcludeGenres))
	if err != nil {
		return fmt.Errorf("settings: discover genres: %w", err)
	}
	networks, err := json.Marshal(cleanedList(global.ExcludeNetworks))
	if err != nil {
		return fmt.Errorf("settings: discover networks: %w", err)
	}
	return setInstanceSettings(ctx, queries, cl, map[string]string{
		KeyDiscoverEnabled: boolPolicy(global.Enabled), KeyDiscoverExcludeGenres: string(genres), KeyDiscoverExcludeNetworks: string(networks),
	})
}

// LibraryDiscovers answers the Discover settings of every library, a library never saved with the defaults and Discover off.
func (c *Cache) LibraryDiscovers(ctx context.Context) ([]LibraryDiscover, error) {
	rows, err := c.queries.ListCategoryDiscover(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: discover libraries: %w", err)
	}
	typeRows, err := c.queries.ListCategoryDiscoverTypes(ctx)
	if err != nil {
		return nil, fmt.Errorf("settings: discover types: %w", err)
	}
	saved := map[int64]settingsdb.CategoryDiscover{}
	for _, row := range rows {
		saved[row.CategoryID] = row
	}
	typesOf := map[int64]map[string]settingsdb.CategoryDiscoverType{}
	for _, row := range typeRows {
		if typesOf[row.CategoryID] == nil {
			typesOf[row.CategoryID] = map[string]settingsdb.CategoryDiscoverType{}
		}
		typesOf[row.CategoryID][row.Type] = row
	}
	var out []LibraryDiscover
	for _, category := range c.Snapshot().Categories() {
		out = append(out, resolveLibraryDiscover(category, saved, typesOf[category.ID]))
	}
	return out, nil
}

// LibraryDiscover answers one library's Discover settings; ErrNotFound for a library that does not exist.
func (c *Cache) LibraryDiscover(ctx context.Context, categoryID int64) (LibraryDiscover, error) {
	all, err := c.LibraryDiscovers(ctx)
	if err != nil {
		return LibraryDiscover{}, err
	}
	for _, one := range all {
		if one.CategoryID == categoryID {
			return one, nil
		}
	}
	return LibraryDiscover{}, fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
}

func resolveLibraryDiscover(category Category, saved map[int64]settingsdb.CategoryDiscover, typeRows map[string]settingsdb.CategoryDiscoverType) LibraryDiscover {
	out := LibraryDiscover{
		CategoryID: category.ID, Name: category.Name, MediaType: category.MediaType, TotalAmount: DefaultDiscoverTotal, ClassicAgeYears: DefaultDiscoverClassicYears,
		MinRating: DefaultDiscoverMinRating, MinVotes: DefaultDiscoverMinVotes, AutoAddPerDay: DefaultDiscoverAutoAddPerDay,
	}
	if row, found := saved[category.ID]; found {
		out.Enabled, out.TotalAmount, out.ClassicAgeYears = row.Enabled == 1, row.TotalAmount, row.ClassicAgeYears
		out.MinRating, out.MinVotes, out.AutoAddPerDay, out.AutoAddSearch = row.MinRating, row.MinVotes, row.AutoAddPerDay, row.AutoAddSearch == 1
		out.OriginalLanguages, out.LanguagesSet = decodeList(row.OriginalLanguages), row.LanguagesSet == 1
		out.ExcludeGenres, out.ExcludeNetworks, out.DisabledKinds = decodeList(row.ExcludeGenres), decodeList(row.ExcludeNetworks), decodeList(row.DisabledKinds)
	}
	for _, name := range DiscoverTypes {
		setting := DiscoverTypeSetting{Type: name, Enabled: true, Amount: DefaultDiscoverTypeAmount}
		if row, found := typeRows[name]; found {
			setting.Enabled, setting.Amount, setting.AutoAdd = row.Enabled == 1, row.Amount, row.AutoAdd == 1
		}
		out.Types = append(out.Types, setting)
	}
	return out
}

func decodeList(raw string) []string {
	if raw == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func encodeList(values []string) (string, error) {
	values = cleanedList(values)
	if len(values) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(values)
	return string(raw), err
}

// cleanedList drops blanks and repeats, keeping the order the person gave.
func cleanedList(values []string) []string {
	out := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !slices.Contains(out, value) {
			out = append(out, value)
		}
	}
	return out
}

// ValidateLibraryDiscover is what a save checks, for a caller that judges settings it does not save.
func ValidateLibraryDiscover(setting LibraryDiscover) config.Problems {
	return validateLibraryDiscover(setting)
}

func validateLibraryDiscover(setting LibraryDiscover) config.Problems {
	var problems config.Problems
	problem := func(path string, value any, expectation string) {
		problems = append(problems, config.Problem{Path: path, Value: fmt.Sprint(value), Expectation: expectation})
	}
	if setting.TotalAmount < 1 || setting.TotalAmount > 500 {
		problem("total_amount", setting.TotalAmount, "1 to 500")
	}
	if setting.ClassicAgeYears < 1 || setting.ClassicAgeYears > 150 {
		problem("classic_age_years", setting.ClassicAgeYears, "1 to 150 years")
	}
	if setting.MinRating < 0 || setting.MinRating > 10 {
		problem("min_rating", setting.MinRating, "0 to 10")
	}
	if setting.MinVotes < 0 {
		problem("min_votes", setting.MinVotes, "0 or more")
	}
	if setting.AutoAddPerDay < 0 || setting.AutoAddPerDay > 100 {
		problem("auto_add_per_day", setting.AutoAddPerDay, "0 to 100")
	}
	for _, one := range setting.Types {
		if !slices.Contains(DiscoverTypes, one.Type) {
			problem("types", one.Type, "one of "+fmt.Sprint(DiscoverTypes))
		}
		if one.Amount < 1 || one.Amount > 200 {
			problem("types."+one.Type+".amount", one.Amount, "1 to 200")
		}
	}
	return problems
}

// SetLibraryDiscover saves one library's Discover settings and every type row it names, in one transaction.
func (c *Cache) SetLibraryDiscover(ctx context.Context, setting LibraryDiscover) error {
	if _, found := c.Snapshot().Category(setting.CategoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, setting.CategoryID)
	}
	if problems := validateLibraryDiscover(setting); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return writeLibraryDiscover(ctx, queries, setting)
	})
}

// writeLibraryDiscover replaces one library's row and its type rows inside the caller's transaction.
func writeLibraryDiscover(ctx context.Context, queries *settingsdb.Queries, setting LibraryDiscover) error {
	languages, err := encodeList(setting.OriginalLanguages)
	if err != nil {
		return fmt.Errorf("settings: discover languages: %w", err)
	}
	genres, err := encodeList(setting.ExcludeGenres)
	if err != nil {
		return fmt.Errorf("settings: discover genres: %w", err)
	}
	networks, err := encodeList(setting.ExcludeNetworks)
	if err != nil {
		return fmt.Errorf("settings: discover networks: %w", err)
	}
	kinds, err := encodeList(setting.DisabledKinds)
	if err != nil {
		return fmt.Errorf("settings: discover kinds: %w", err)
	}
	if err := queries.UpsertCategoryDiscover(ctx, settingsdb.UpsertCategoryDiscoverParams{
		CategoryID: setting.CategoryID, Enabled: boolToInt(setting.Enabled), TotalAmount: setting.TotalAmount, ClassicAgeYears: setting.ClassicAgeYears,
		MinRating: setting.MinRating, MinVotes: setting.MinVotes, AutoAddPerDay: setting.AutoAddPerDay, AutoAddSearch: boolToInt(setting.AutoAddSearch),
		OriginalLanguages: languages, LanguagesSet: boolToInt(setting.LanguagesSet), ExcludeGenres: genres, ExcludeNetworks: networks, DisabledKinds: kinds,
	}); err != nil {
		return err
	}
	if err := queries.DeleteCategoryDiscoverTypes(ctx, setting.CategoryID); err != nil {
		return err
	}
	for _, one := range setting.Types {
		if err := queries.AddCategoryDiscoverType(ctx, settingsdb.AddCategoryDiscoverTypeParams{
			CategoryID: setting.CategoryID, Type: one.Type, Enabled: boolToInt(one.Enabled), Amount: one.Amount, AutoAdd: boolToInt(one.AutoAdd),
		}); err != nil {
			return err
		}
	}
	return nil
}
