package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/db/store/gen/settingsdb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Reference is one kind of thing holding on to an entity, and how many there are (§6).
type Reference struct {
	Kind  string
	Count int64
}

// InUseError reports a refused deletion and everything that caused it.
type InUseError struct {
	Entity     string
	ID         int64
	References []Reference
}

func (e *InUseError) Error() string {
	parts := make([]string, 0, len(e.References))
	for _, reference := range e.References {
		parts = append(parts, fmt.Sprintf("%d %s", reference.Count, reference.Kind))
	}
	return fmt.Sprintf("settings: %s %d is still referenced by %s", e.Entity, e.ID, strings.Join(parts, " and "))
}

func (e *InUseError) Unwrap() error { return ErrInUse }

// CreateCategory adds a category and grants every administrator manage on it (ADR-27).
func (c *Cache) CreateCategory(ctx context.Context, mediaType, name string, priorityRank int64) (Category, error) {
	if problems := validateCategory(mediaType, name, priorityRank); len(problems) > 0 {
		return Category{}, problems
	}
	// category(media_type, name) is UNIQUE (migration 0001); checked here
	// rather than left to that constraint's own raw error, which writeError
	// has nothing to map and so answers a bare "the request could not be
	// completed" -- found live via the setup wizard's own *Arr connect step
	// re-submitted against a category that already existed.
	for _, existing := range c.Snapshot().Categories() {
		if existing.MediaType == mediaType && existing.Name == name {
			return Category{}, config.Problems{{
				Path: "name", Value: name,
				Expectation: fmt.Sprintf("a name not already used by another %s category", mediaType),
			}}
		}
	}

	grants := c.newLibraryGrants()

	var created settingsdb.Category
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateCategory(ctx, settingsdb.CreateCategoryParams{
			MediaType: mediaType, Name: name, PriorityRank: priorityRank,
		})
		if err != nil {
			return err
		}
		created = row
		// A library starts with the providers that suit its type, so its order is its own from the first day.
		for index, source := range DefaultMetadataSources(mediaType, "") {
			if err := queries.InsertCategoryMetadataSource(ctx, settingsdb.InsertCategoryMetadataSourceParams{CategoryID: row.ID, Source: source, Ord: int64(index)}); err != nil {
				return err
			}
		}
		for userID, permission := range grants {
			if err := queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
				UserID: userID, CategoryID: row.ID, Permission: string(permission),
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Category{}, fmt.Errorf("settings: create category %q: %w", name, err)
	}
	return Category{ID: created.ID, MediaType: created.MediaType, Name: created.Name,
		PriorityRank: created.PriorityRank, MonitorDiscovered: created.MonitorDiscovered, TVMonitor: created.TvMonitor, Kind: created.Kind,
		MonitorReleaseTypes: decodeReleaseTypes(created.MonitorReleaseTypes)}, nil
}

// UpdateCategory renames a category, changes its priority rank, and sets its
// default quality/language profile and monitor-discovered policy, all as one
// write. media_type never changes -- it is the category's own identity
// alongside name (the same UNIQUE(media_type, name) CreateCategory's own
// duplicate check protects), not a policy field. Todo 4.6's own remaining
// gap: creation and deletion existed, editing did not (queries.UpdateCategory
// itself was scaffolded ahead of this caller, the same "ready ahead of the
// consumer" shape quality/language profiles and placement.Resolve already
// had, 2026-09-12). One method rather than SetCategoryQualityProfile/
// SetCategoryLanguageProfile/SetCategoryMonitorDiscovered called separately
// by the caller (those three still stand alone for a future caller that
// wants just one of them): an apiserver handler that wrote through each and
// then read its own (necessarily request-frozen, ADR-27) Access back for the
// response would see the category as it was when the request began, not
// what it had just written -- found live verifying this exact endpoint.
// qualityProfileID/languageProfileID nil clears the default.
func (c *Cache) UpdateCategory(
	ctx context.Context, id int64, name string, priorityRank int64,
	qualityProfileID, languageProfileID *int64, monitorDiscovered string,
) (Category, error) {
	snapshot := c.Snapshot()
	existing, found := snapshot.Category(id)
	if !found {
		return Category{}, fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	var problems config.Problems
	problems = append(problems, validateCategory(existing.MediaType, name, priorityRank)...)
	problems = append(problems, validateMonitorDiscovered(monitorDiscovered)...)
	if len(problems) > 0 {
		return Category{}, problems
	}
	if qualityProfileID != nil {
		if _, found := snapshot.QualityProfile(*qualityProfileID); !found {
			return Category{}, fmt.Errorf("%w: quality profile %d", ErrNotFound, *qualityProfileID)
		}
	}
	if languageProfileID != nil {
		if _, found := snapshot.LanguageProfile(*languageProfileID); !found {
			return Category{}, fmt.Errorf("%w: language profile %d", ErrNotFound, *languageProfileID)
		}
	}
	for _, other := range snapshot.Categories() {
		if other.ID != id && other.MediaType == existing.MediaType && other.Name == name {
			return Category{}, config.Problems{{
				Path: "name", Value: name,
				Expectation: fmt.Sprintf("a name not already used by another %s category", existing.MediaType),
			}}
		}
	}

	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := queries.UpdateCategory(ctx, settingsdb.UpdateCategoryParams{
			Name: name, PriorityRank: priorityRank, ID: id,
		}); err != nil {
			return err
		}
		if err := writeQualityList(ctx, queries, id, withFirst(existing.QualityProfileIDs, qualityProfileID),
			existing.QualityCutoffID); err != nil {
			return err
		}
		if err := queries.SetCategoryLanguageProfile(ctx, settingsdb.SetCategoryLanguageProfileParams{
			LanguageProfileID: int64PtrToNull(languageProfileID), ID: id,
		}); err != nil {
			return err
		}
		return queries.SetCategoryMonitorDiscovered(ctx,
			settingsdb.SetCategoryMonitorDiscoveredParams{MonitorDiscovered: monitorDiscovered, ID: id})
	})
	if err != nil {
		return Category{}, fmt.Errorf("settings: update category %d: %w", id, err)
	}
	updated, ok := c.Snapshot().Category(id)
	if !ok {
		return Category{}, fmt.Errorf("settings: category %d vanished after update", id)
	}
	return updated, nil
}

// SetCategoryMonitorDiscovered sets what a scan does with an album it finds on
// disk that nobody asked for (section 19.6, todo 4.4) -- chosen per category,
// not globally, so the live and test libraries can differ.
func (c *Cache) SetCategoryMonitorDiscovered(ctx context.Context, id int64, policy string) error {
	if _, found := c.Snapshot().Category(id); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	if problems := validateMonitorDiscovered(policy); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryMonitorDiscovered(ctx,
			settingsdb.SetCategoryMonitorDiscoveredParams{MonitorDiscovered: policy, ID: id})
	})
}

// SetCategorySearchShare sets a library's share of the automatic searches, 0 (none) to 100.
func (c *Cache) SetCategorySearchShare(ctx context.Context, id, share int64) error {
	if _, found := c.Snapshot().Category(id); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	if share < 0 || share > maxSearchShare {
		return config.Problems{{Path: "search_share", Value: strconv.FormatInt(share, 10),
			Expectation: "0 (none) to " + strconv.Itoa(maxSearchShare)}}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategorySearchShare(ctx, settingsdb.SetCategorySearchShareParams{SearchShare: share, ID: id})
	})
}

// maxSearchShare bounds a library's search share; a weight, so only the ratios matter.
const maxSearchShare = 100

// SetCategoryKind records a library's kind; which kinds a media type has is its own declaration, checked by the caller.
func (c *Cache) SetCategoryKind(ctx context.Context, id int64, kind string) (Category, error) {
	before, found := c.Snapshot().Category(id)
	if !found {
		return Category{}, fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	if problems := validateKind(kind); len(problems) > 0 {
		return Category{}, problems
	}
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := queries.SetCategoryKind(ctx, settingsdb.SetCategoryKindParams{Kind: kind, ID: id}); err != nil {
			return err
		}
		// A library still on its type's starting order moves to the starting order of its new kind: an anime library asks AniList first.
		if slices.Equal(before.MetadataSources, DefaultMetadataSources(before.MediaType, before.Kind)) {
			if err := queries.DeleteCategoryMetadataSources(ctx, id); err != nil {
				return err
			}
			for index, source := range DefaultMetadataSources(before.MediaType, kind) {
				if err := queries.InsertCategoryMetadataSource(ctx, settingsdb.InsertCategoryMetadataSourceParams{CategoryID: id, Source: source, Ord: int64(index)}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return Category{}, err
	}
	updated, _ := c.Snapshot().Category(id)
	return updated, nil
}

// SetCategoryRatingLimit sets a library's maximum rating as an age (nil for none) and whether unrated works may be in it.
func (c *Cache) SetCategoryRatingLimit(ctx context.Context, id int64, maxAge *int64, allowUnrated bool) (Category, error) {
	if _, found := c.Snapshot().Category(id); !found {
		return Category{}, fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	if maxAge != nil && (*maxAge < 0 || *maxAge > maxRatingAge) {
		return Category{}, config.Problems{{Path: "rating_limit.max_age", Value: strconv.FormatInt(*maxAge, 10),
			Expectation: "an age from 0 to " + strconv.Itoa(maxRatingAge) + ", or none"}}
	}
	allowed := int64(0)
	if allowUnrated {
		allowed = 1
	}
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryRatingLimit(ctx, settingsdb.SetCategoryRatingLimitParams{
			MaxAge: nullInt64(maxAge), AllowUnrated: allowed, ID: id,
		})
	})
	if err != nil {
		return Category{}, err
	}
	updated, _ := c.Snapshot().Category(id)
	return updated, nil
}

// maxRatingAge is the oldest age a rating names (R21).
const maxRatingAge = 21

// SetCategoryTVMonitor sets what a scan monitors of a series it finds in a TV library, answering the category as written.
func (c *Cache) SetCategoryTVMonitor(ctx context.Context, id int64, policy string) (Category, error) {
	if _, found := c.Snapshot().Category(id); !found {
		return Category{}, fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	if problems := validateTVMonitor(policy); len(problems) > 0 {
		return Category{}, problems
	}
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryTVMonitor(ctx, settingsdb.SetCategoryTVMonitorParams{TvMonitor: policy, ID: id})
	})
	if err != nil {
		return Category{}, err
	}
	updated, _ := c.Snapshot().Category(id)
	return updated, nil
}

// DeleteCategory removes a category, or refuses and says what still uses it (Invariant 41).
func (c *Cache) DeleteCategory(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().Category(id); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}

	roots, err := c.queries.CountRootFoldersInCategory(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: count roots for category %d: %w", id, err)
	}
	grants, err := c.queries.CountUsersWithCategoryAccess(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: count access for category %d: %w", id, err)
	}
	discoveryJobs, err := c.queries.CountDiscoveryJobsUsingCategory(ctx, sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		return fmt.Errorf("settings: count discovery jobs for category %d: %w", id, err)
	}

	var references []Reference
	if roots > 0 {
		references = append(references, Reference{Kind: "attached root folders", Count: roots})
	}
	if grants > 0 {
		references = append(references, Reference{Kind: "user access grants", Count: grants})
	}
	if discoveryJobs > 0 {
		references = append(references, Reference{Kind: "discovery jobs", Count: discoveryJobs})
	}
	if len(references) > 0 {
		return &InUseError{Entity: "category", ID: id, References: references}
	}

	current, err := c.queries.GetCategory(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: read category %d: %w", id, err)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := queries.DeleteCategory(ctx, id); err != nil {
			return err
		}
		// A library's naming profile is its own; it goes with the library.
		if current.NamingProfileID.Valid {
			return queries.DeleteNamingProfile(ctx, current.NamingProfileID.Int64)
		}
		return nil
	})
}

// CreateQualityProfile adds a quality profile (ADR-40). Deep validation of
// rulesJSON's shape belongs to internal/identify/score, the only package that interprets
// it; this layer checks only what settings.db itself must never hold: a name and
// syntactically valid JSON (ADR-44 -- a bad policy must be refused on write, not
// discovered later at startup).
func (c *Cache) CreateQualityProfile(ctx context.Context, name, rulesJSON, upgradeJSON string) (QualityProfile, error) {
	return c.CreateQualityProfileFor(ctx, "music", name, rulesJSON, upgradeJSON)
}

// CreateQualityProfileFor adds a quality profile judging one media type.
func (c *Cache) CreateQualityProfileFor(
	ctx context.Context, mediaType, name, rulesJSON, upgradeJSON string,
) (QualityProfile, error) {
	problems := validateQualityProfile(name, rulesJSON, upgradeJSON)
	if !slices.Contains(MediaTypes, mediaType) {
		problems = append(problems, config.Problem{Path: "media_type", Value: mediaType, Expectation: "one of " + strings.Join(MediaTypes, ", ")})
	}
	if len(problems) > 0 {
		return QualityProfile{}, problems
	}

	var created settingsdb.QualityProfile
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateQualityProfile(ctx, settingsdb.CreateQualityProfileParams{
			Name: name, RulesJson: rulesJSON, UpgradeJson: upgradeJSON, MediaType: mediaType,
		})
		created = row
		return err
	})
	if err != nil {
		return QualityProfile{}, fmt.Errorf("settings: create quality profile %q: %w", name, err)
	}
	return QualityProfile{ID: created.ID, Name: created.Name, RulesJSON: created.RulesJson,
		UpgradeJSON: created.UpgradeJson, MediaType: created.MediaType}, nil
}

// UpdateQualityProfile replaces a quality profile's policy.
func (c *Cache) UpdateQualityProfile(ctx context.Context, id int64, name, rulesJSON, upgradeJSON string) error {
	if _, found := c.Snapshot().QualityProfile(id); !found {
		return fmt.Errorf("%w: quality profile %d", ErrNotFound, id)
	}
	if problems := validateQualityProfile(name, rulesJSON, upgradeJSON); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateQualityProfile(ctx, settingsdb.UpdateQualityProfileParams{
			Name: name, RulesJson: rulesJSON, UpgradeJson: upgradeJSON, ID: id,
		})
	})
}

// DeleteQualityProfile removes a quality profile, or refuses and says which
// categories still default to it. A desired_item in library.db may also still
// reference it (ADR-25 forbids the cross-database FK that would let this check
// see that) -- not yet possible to create, since desired_item creation for music
// is itself blocked on this profile system existing at all.
func (c *Cache) DeleteQualityProfile(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().QualityProfile(id); !found {
		return fmt.Errorf("%w: quality profile %d", ErrNotFound, id)
	}
	categories, err := c.queries.CountCategoryListsUsingQualityProfile(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: count categories for quality profile %d: %w", id, err)
	}
	if categories > 0 {
		return &InUseError{Entity: "quality profile", ID: id,
			References: []Reference{{Kind: "categories", Count: categories}}}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteQualityProfile(ctx, id)
	})
}

// CreateLanguageProfile adds a language profile (section 19.8).
func (c *Cache) CreateLanguageProfile(ctx context.Context, profile LanguageProfile) (LanguageProfile, error) {
	if problems := validateLanguageProfile(profile.Name); len(problems) > 0 {
		return LanguageProfile{}, problems
	}
	audioPreferred, err := encodeStringArray(profile.AudioPreferred)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode audio_preferred: %w", err)
	}
	audioRequired, err := encodeOptionalStringArray(profile.AudioRequired)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode audio_required: %w", err)
	}
	subtitlePreferred, err := encodeOptionalStringArray(profile.SubtitlePreferred)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode subtitle_preferred: %w", err)
	}
	subtitleRequired, err := encodeOptionalStringArray(profile.SubtitleRequired)
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: encode subtitle_required: %w", err)
	}

	var created settingsdb.LanguageProfile
	err = c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateLanguageProfile(ctx, settingsdb.CreateLanguageProfileParams{
			Name:                  profile.Name,
			AudioPreferredJson:    audioPreferred,
			AudioRequiredJson:     audioRequired,
			SubtitlePreferredJson: subtitlePreferred,
			SubtitleRequiredJson:  subtitleRequired,
			PreferOriginalAudio:   boolToInt(profile.PreferOriginalAudio),
			RejectDubbed:          boolToInt(profile.RejectDubbed),
		})
		created = row
		return err
	})
	if err != nil {
		return LanguageProfile{}, fmt.Errorf("settings: create language profile %q: %w", profile.Name, err)
	}
	saved, found := c.Snapshot().LanguageProfile(created.ID)
	if !found {
		return LanguageProfile{}, fmt.Errorf("settings: language profile %d vanished after create", created.ID)
	}
	return saved, nil
}

// UpdateLanguageProfile replaces a language profile's policy.
func (c *Cache) UpdateLanguageProfile(ctx context.Context, id int64, profile LanguageProfile) error {
	if _, found := c.Snapshot().LanguageProfile(id); !found {
		return fmt.Errorf("%w: language profile %d", ErrNotFound, id)
	}
	if problems := validateLanguageProfile(profile.Name); len(problems) > 0 {
		return problems
	}
	audioPreferred, err := encodeStringArray(profile.AudioPreferred)
	if err != nil {
		return fmt.Errorf("settings: encode audio_preferred: %w", err)
	}
	audioRequired, err := encodeOptionalStringArray(profile.AudioRequired)
	if err != nil {
		return fmt.Errorf("settings: encode audio_required: %w", err)
	}
	subtitlePreferred, err := encodeOptionalStringArray(profile.SubtitlePreferred)
	if err != nil {
		return fmt.Errorf("settings: encode subtitle_preferred: %w", err)
	}
	subtitleRequired, err := encodeOptionalStringArray(profile.SubtitleRequired)
	if err != nil {
		return fmt.Errorf("settings: encode subtitle_required: %w", err)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateLanguageProfile(ctx, settingsdb.UpdateLanguageProfileParams{
			Name:                  profile.Name,
			AudioPreferredJson:    audioPreferred,
			AudioRequiredJson:     audioRequired,
			SubtitlePreferredJson: subtitlePreferred,
			SubtitleRequiredJson:  subtitleRequired,
			PreferOriginalAudio:   boolToInt(profile.PreferOriginalAudio),
			RejectDubbed:          boolToInt(profile.RejectDubbed),
			ID:                    id,
		})
	})
}

// DeleteLanguageProfile removes a language profile, or refuses and says which
// categories still default to it.
func (c *Cache) DeleteLanguageProfile(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().LanguageProfile(id); !found {
		return fmt.Errorf("%w: language profile %d", ErrNotFound, id)
	}
	categories, err := c.queries.CountCategoriesUsingLanguageProfile(ctx, sql.NullInt64{Int64: id, Valid: true})
	if err != nil {
		return fmt.Errorf("settings: count categories for language profile %d: %w", id, err)
	}
	if categories > 0 {
		return &InUseError{Entity: "language profile", ID: id,
			References: []Reference{{Kind: "categories", Count: categories}}}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteLanguageProfile(ctx, id)
	})
}

// SetCategoryQualityProfile sets or clears (nil) a category's default quality profile.
func (c *Cache) SetCategoryQualityProfile(ctx context.Context, categoryID int64, profileID *int64) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	if profileID != nil {
		if _, found := snapshot.QualityProfile(*profileID); !found {
			return fmt.Errorf("%w: quality profile %d", ErrNotFound, *profileID)
		}
	}
	category, _ := snapshot.Category(categoryID)
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return writeQualityList(ctx, queries, categoryID, withFirst(category.QualityProfileIDs, profileID),
			category.QualityCutoffID)
	})
}

// SetCategoryQualityUpgrades turns a library's upgrades on or off.
func (c *Cache) SetCategoryQualityUpgrades(ctx context.Context, categoryID int64, allowed bool) error {
	if _, found := c.Snapshot().Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	value := int64(0)
	if allowed {
		value = 1
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryQualityUpgrades(ctx, settingsdb.SetCategoryQualityUpgradesParams{QualityUpgrades: value, ID: categoryID})
	})
}

// maxProtocolDelayMinutes is a week: a longer wait is a filter in all but name.
const maxProtocolDelayMinutes = 7 * 24 * 60

// SetCategoryProtocolDelay saves a library's delay profile: protocol is "usenet", "torrent" or "" for none.
func (c *Cache) SetCategoryProtocolDelay(ctx context.Context, categoryID int64, protocol string, minutes int64) error {
	if _, found := c.Snapshot().Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	var problems config.Problems
	if protocol != "" && protocol != "usenet" && protocol != "torrent" {
		problems = append(problems, config.Problem{Path: "preferred_protocol", Value: protocol, Expectation: "usenet, torrent or empty"})
	}
	if minutes < 0 || minutes > maxProtocolDelayMinutes {
		problems = append(problems, config.Problem{
			Path: "protocol_delay_minutes", Value: strconv.FormatInt(minutes, 10), Expectation: "0 to " + strconv.Itoa(maxProtocolDelayMinutes),
		})
	}
	if len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryProtocolDelay(ctx, settingsdb.SetCategoryProtocolDelayParams{
			PreferredProtocol: protocol, ProtocolDelayMinutes: minutes, ID: categoryID,
		})
	})
}

// SetCategoryQuality saves a library's quality: the profiles it accepts in priority order, the
// cutoff (one of them), and the id of the quality target it aims for, nil for none. Every problem is reported at once.
func (c *Cache) SetCategoryQuality(
	ctx context.Context, categoryID int64, profileIDs []int64, cutoffID *int64, targetID *int64,
) error {
	snapshot := c.Snapshot()
	category, found := snapshot.Category(categoryID)
	if !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	var problems config.Problems
	seen := make(map[int64]bool, len(profileIDs))
	for index, id := range profileIDs {
		where := fmt.Sprintf("profiles[%d]", index)
		if profile, found := snapshot.QualityProfile(id); !found {
			problems = append(problems, config.Problem{Path: where, Value: strconv.FormatInt(id, 10),
				Expectation: "an existing quality profile"})
		} else if profile.MediaType != category.MediaType {
			problems = append(problems, config.Problem{Path: where, Value: profile.Name,
				Expectation: "a " + category.MediaType + " quality profile"})
		}
		if seen[id] {
			problems = append(problems, config.Problem{Path: where, Value: strconv.FormatInt(id, 10),
				Expectation: "a profile listed once"})
		}
		seen[id] = true
	}
	if cutoffID != nil && !seen[*cutoffID] {
		problems = append(problems, config.Problem{Path: "cutoff", Value: strconv.FormatInt(*cutoffID, 10),
			Expectation: "one of the library's own profiles"})
	}
	if targetID != nil {
		if target, found := snapshot.QualityTarget(*targetID); !found {
			problems = append(problems, config.Problem{Path: "target", Value: strconv.FormatInt(*targetID, 10), Expectation: "an existing quality target"})
		} else if target.MediaType != category.MediaType {
			problems = append(problems, config.Problem{Path: "target", Value: target.Name, Expectation: "a " + category.MediaType + " quality target"})
		}
	}
	if len(problems) > 0 {
		return problems
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := writeQualityList(ctx, queries, categoryID, profileIDs, cutoffID); err != nil {
			return err
		}
		if targetID == nil {
			return queries.ClearCategoryTarget(ctx, categoryID)
		}
		return queries.SetCategoryTarget(ctx, settingsdb.SetCategoryTargetParams{CategoryID: categoryID, TargetID: *targetID})
	})
}

// CreateQualityTarget adds a named target a library may aim for; every problem is reported at once.
func (c *Cache) CreateQualityTarget(ctx context.Context, target QualityTarget) (QualityTarget, error) {
	if problems := c.validateTargetName(target, 0); len(problems) > 0 {
		return QualityTarget{}, problems
	}
	var id int64
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		created, err := queries.CreateQualityTarget(ctx, target.createParams())
		id = created
		return err
	})
	if err != nil {
		return QualityTarget{}, fmt.Errorf("settings: create quality target %q: %w", target.Name, err)
	}
	target.ID = id
	return target, nil
}

// UpdateQualityTarget replaces a target; the libraries that picked it follow.
func (c *Cache) UpdateQualityTarget(ctx context.Context, target QualityTarget) error {
	existing, found := c.Snapshot().QualityTarget(target.ID)
	if !found {
		return fmt.Errorf("%w: quality target %d", ErrNotFound, target.ID)
	}
	target.MediaType = existing.MediaType
	if problems := c.validateTargetName(target, target.ID); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateQualityTarget(ctx, target.updateParams())
	})
}

// DeleteQualityTarget removes a target no library uses.
func (c *Cache) DeleteQualityTarget(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().QualityTarget(id); !found {
		return fmt.Errorf("%w: quality target %d", ErrNotFound, id)
	}
	for _, category := range c.Snapshot().Categories() {
		if category.QualityTarget != nil && category.QualityTarget.ID == id {
			return fmt.Errorf("%w: library %s still aims for this target", ErrInUse, category.Name)
		}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteQualityTarget(ctx, id)
	})
}

// validateTargetName is validateQualityTargetNamed plus the name being free among the media type's targets.
func (c *Cache) validateTargetName(target QualityTarget, selfID int64) config.Problems {
	problems := validateQualityTargetNamed(&target)
	for _, other := range c.Snapshot().QualityTargets() {
		if other.ID != selfID && other.MediaType == target.MediaType && strings.EqualFold(other.Name, target.Name) {
			problems = append(problems, config.Problem{Path: "target.name", Value: target.Name, Expectation: "a name no other " + target.MediaType + " target has"})
		}
	}
	return problems
}

// validateQualityTargetNamed also asks for a name and a known media type.
func validateQualityTargetNamed(target *QualityTarget) config.Problems {
	var problems config.Problems
	if strings.TrimSpace(target.Name) == "" {
		problems = append(problems, config.Problem{Path: "target.name", Expectation: "a name"})
	}
	if !slices.Contains(MediaTypes, target.MediaType) {
		problems = append(problems, config.Problem{Path: "target.media_type", Value: target.MediaType, Expectation: "one of " + strings.Join(MediaTypes, ", ")})
	}
	return append(problems, validateQualityTarget(target)...)
}

// maxTargetTolerancePct keeps "fuzzy" a range around the bitrate rather than any bitrate at all.
const maxTargetTolerancePct = 50

func validateQualityTarget(target *QualityTarget) config.Problems {
	if target == nil {
		return nil
	}
	var problems config.Problems
	video := target.IsVideo()
	if !video && strings.TrimSpace(target.AudioCodec) == "" {
		problems = append(problems, config.Problem{Path: "target.audio_codec", Expectation: "a codec, e.g. aac (or a resolution or video codec for a video library)"})
	}
	if !video && strings.TrimSpace(target.Container) == "" {
		problems = append(problems, config.Problem{Path: "target.container", Expectation: "a file type, e.g. m4a"})
	}
	if target.BitrateKbps != nil && *target.BitrateKbps <= 0 {
		problems = append(problems, config.Problem{Path: "target.bitrate_kbps",
			Value: strconv.FormatInt(*target.BitrateKbps, 10), Expectation: "a bitrate above 0, or none for lossless"})
	}
	for path, kbps := range map[string]*int64{"target.audio_bitrate_kbps": target.AudioBitrateKbps, "target.video_bitrate_kbps": target.VideoBitrateKbps} {
		if kbps != nil && *kbps <= 0 {
			problems = append(problems, config.Problem{Path: path, Value: strconv.FormatInt(*kbps, 10), Expectation: "a bitrate above 0, or none"})
		}
	}
	if target.CRF < 0 || target.CRF > 51 {
		problems = append(problems, config.Problem{Path: "target.crf", Value: strconv.FormatFloat(target.CRF, 'f', -1, 64), Expectation: "a CRF from 0 to 51, or none"})
	}
	if target.FrameRate < 0 || target.FrameRate > 240 {
		problems = append(problems, config.Problem{Path: "target.frame_rate", Value: strconv.FormatFloat(target.FrameRate, 'f', -1, 64), Expectation: "frames a second up to 240, or none"})
	}
	for path, languages := range map[string][]string{"target.audio_languages": target.AudioLanguages, "target.subtitle_languages": target.SubtitleLanguages} {
		for _, language := range languages {
			if len(language) < 2 || len(language) > 3 {
				problems = append(problems, config.Problem{Path: path, Value: language, Expectation: "language codes such as en or de"})
			}
		}
	}
	if target.TolerancePct < 0 || target.TolerancePct > maxTargetTolerancePct {
		problems = append(problems, config.Problem{Path: "target.tolerance_pct",
			Value: strconv.FormatInt(target.TolerancePct, 10), Expectation: "0 to 50 percent"})
	}
	return problems
}

// withFirst puts profileID at the head of the list, keeping the rest in order; nil clears it.
func withFirst(list []int64, profileID *int64) []int64 {
	if profileID == nil {
		return nil
	}
	ordered := []int64{*profileID}
	for _, id := range list {
		if id != *profileID {
			ordered = append(ordered, id)
		}
	}
	return ordered
}

// writeQualityList replaces a library's ordered profiles, keeping category.quality_profile_id
// equal to the first and dropping a cutoff that is no longer in the list.
func writeQualityList(
	ctx context.Context, queries *settingsdb.Queries, categoryID int64, profileIDs []int64, cutoffID *int64,
) error {
	if err := queries.DeleteCategoryQualityProfiles(ctx, categoryID); err != nil {
		return fmt.Errorf("settings: clear quality profiles of category %d: %w", categoryID, err)
	}
	for ord, id := range profileIDs {
		if err := queries.InsertCategoryQualityProfile(ctx, settingsdb.InsertCategoryQualityProfileParams{
			CategoryID: categoryID, QualityProfileID: id, Ord: int64(ord),
		}); err != nil {
			return fmt.Errorf("settings: add quality profile %d to category %d: %w", id, categoryID, err)
		}
	}
	var first *int64
	if len(profileIDs) > 0 {
		first = &profileIDs[0]
	}
	if err := queries.SetCategoryQualityProfile(ctx, settingsdb.SetCategoryQualityProfileParams{
		QualityProfileID: int64PtrToNull(first), ID: categoryID,
	}); err != nil {
		return err
	}
	if cutoffID != nil && !slices.Contains(profileIDs, *cutoffID) {
		cutoffID = nil
	}
	return queries.SetCategoryQualityCutoff(ctx, settingsdb.SetCategoryQualityCutoffParams{
		QualityCutoffProfileID: int64PtrToNull(cutoffID), ID: categoryID,
	})
}

// SetCategoryLanguageProfile sets or clears (nil) a category's default language profile.
func (c *Cache) SetCategoryLanguageProfile(ctx context.Context, categoryID int64, profileID *int64) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	if profileID != nil {
		if _, found := snapshot.LanguageProfile(*profileID); !found {
			return fmt.Errorf("%w: language profile %d", ErrNotFound, *profileID)
		}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryLanguageProfile(ctx, settingsdb.SetCategoryLanguageProfileParams{
			LanguageProfileID: int64PtrToNull(profileID), ID: categoryID,
		})
	})
}

// SetCategoryNaming saves a library's naming, creating its own profile on first save.
func (c *Cache) SetCategoryNaming(ctx context.Context, categoryID int64, profile NamingProfile) error {
	category, found := c.Snapshot().Category(categoryID)
	if !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}
	if c.validateNaming != nil {
		if err := c.validateNaming(category.MediaType, profile); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidNaming, err)
		}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return writeCategoryNaming(ctx, queries, categoryID, profile)
	})
}

// writeCategoryNaming is SetCategoryNaming's write, shared with Import: the library's own
// profile is updated, or created and attached on first save; its formats are replaced.
func writeCategoryNaming(
	ctx context.Context, queries *settingsdb.Queries, categoryID int64, profile NamingProfile,
) error {
	current, err := queries.GetCategory(ctx, categoryID)
	if err != nil {
		return fmt.Errorf("settings: read category %d: %w", categoryID, err)
	}
	profileID := current.NamingProfileID.Int64
	if current.NamingProfileID.Valid {
		err := queries.UpdateNamingProfile(ctx, settingsdb.UpdateNamingProfileParams{
			RenameFiles:              boolToInt(profile.RenameFiles),
			ReplaceIllegalCharacters: boolToInt(profile.ReplaceIllegalCharacters),
			ColonReplacement:         profile.ColonReplacement,
			ID:                       profileID,
		})
		if err != nil {
			return fmt.Errorf("settings: update naming profile %d: %w", profileID, err)
		}
	} else {
		created, err := queries.CreateNamingProfile(ctx, settingsdb.CreateNamingProfileParams{
			Name:                     fmt.Sprintf("library-%d", categoryID),
			RenameFiles:              boolToInt(profile.RenameFiles),
			ReplaceIllegalCharacters: boolToInt(profile.ReplaceIllegalCharacters),
			ColonReplacement:         profile.ColonReplacement,
		})
		if err != nil {
			return fmt.Errorf("settings: create naming profile for category %d: %w", categoryID, err)
		}
		profileID = created
		err = queries.SetCategoryNamingProfile(ctx, settingsdb.SetCategoryNamingProfileParams{
			NamingProfileID: sql.NullInt64{Int64: profileID, Valid: true}, ID: categoryID,
		})
		if err != nil {
			return fmt.Errorf("settings: attach naming profile to category %d: %w", categoryID, err)
		}
	}

	if err := queries.DeleteNamingFormats(ctx, profileID); err != nil {
		return fmt.Errorf("settings: clear naming formats %d: %w", profileID, err)
	}
	for _, slot := range slices.Sorted(maps.Keys(profile.Formats)) {
		err := queries.InsertNamingFormat(ctx, settingsdb.InsertNamingFormatParams{
			NamingProfileID: profileID, Slot: slot, Template: profile.Formats[slot],
		})
		if err != nil {
			return fmt.Errorf("settings: save naming format %s: %w", slot, err)
		}
	}
	return nil
}

func int64PtrToNull(id *int64) sql.NullInt64 {
	if id == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *id, Valid: true}
}

func encodeStringArray(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func encodeOptionalStringArray(values []string) (sql.NullString, error) {
	if len(values) == 0 {
		return sql.NullString{}, nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(raw), Valid: true}, nil
}

// CreateRootFolder adds a root folder.
func (c *Cache) CreateRootFolder(ctx context.Context, path, label string) (RootFolder, error) {
	if problems := validateRootFolder(path); len(problems) > 0 {
		return RootFolder{}, problems
	}

	var created settingsdb.RootFolder
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateRootFolder(ctx, settingsdb.CreateRootFolderParams{
			Path:         path,
			Label:        sql.NullString{String: label, Valid: label != ""},
			Online:       1,
			MinFreeBytes: DefaultMinFreeBytes,
		})
		created = row
		return err
	})
	if err != nil {
		return RootFolder{}, fmt.Errorf("settings: create root folder %q: %w", path, err)
	}
	return convertRootFolder(created), nil
}

// SetRootFolderPolicy writes the user's own placement policy for one root
// (section 11.7, Invariant 69) -- read_only, allow_rename and the reserve.
// Never touched by the capacity job, which only ever writes probed facts
// via SetRootFolderCapacity.
func (c *Cache) SetRootFolderPolicy(ctx context.Context, id int64, readOnly, allowRename bool, minFreeBytes int64) error {
	if _, found := c.Snapshot().RootFolder(id); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, id)
	}
	if problems := validateRootFolderPolicy(minFreeBytes); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderPolicy(ctx, settingsdb.SetRootFolderPolicyParams{
			ReadOnly: boolToInt(readOnly), AllowRename: boolToInt(allowRename),
			MinFreeBytes: minFreeBytes, ID: id,
		})
	})
}

// RootFolderCapacity is everything a capacity probe found about one root,
// for SetRootFolderCapacity -- grouped so the job that calls it (the only
// writer of these columns) passes one value rather than seven positional
// ones.
type RootFolderCapacity struct {
	Writable         bool
	WritableReason   string
	TotalBytes       int64
	FreeBytes        int64
	CheckedAt        int64
	FSID             string
	CapabilitiesJSON string
}

// SetRootFolderCapacity records what the capacity-polling job found --
// section 11.7: "capacity is polled, not assumed". The only writer of
// writable/total_bytes/free_bytes/fs_id; user policy lives in
// SetRootFolderPolicy instead, so the two paths can never race each other's
// columns.
func (c *Cache) SetRootFolderCapacity(ctx context.Context, id int64, capacity RootFolderCapacity) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderCapacity(ctx, settingsdb.SetRootFolderCapacityParams{
			Writable:          boolToInt(capacity.Writable),
			WritableReason:    sql.NullString{String: capacity.WritableReason, Valid: capacity.WritableReason != ""},
			TotalBytes:        sql.NullInt64{Int64: capacity.TotalBytes, Valid: true},
			FreeBytes:         sql.NullInt64{Int64: capacity.FreeBytes, Valid: true},
			CapacityCheckedAt: sql.NullInt64{Int64: capacity.CheckedAt, Valid: true},
			FsID:              sql.NullString{String: capacity.FSID, Valid: capacity.FSID != ""},
			CapabilitiesJson:  sql.NullString{String: capacity.CapabilitiesJSON, Valid: capacity.CapabilitiesJSON != ""},
			ID:                id,
		})
	})
}

// SetAcceptsNewForRootFolder flips accepts_new across every category this
// root is attached to -- ADR-51's own "draining forces accepts_new = 0"
// (section 11.7), applied uniformly since a drain evacuates the whole disk,
// not one category's share of it. Reuses AttachRootFolder's own upsert per
// attachment rather than a new query, preserving each attachment's ord and
// weight.
func (c *Cache) SetAcceptsNewForRootFolder(ctx context.Context, rootFolderID int64, acceptsNew bool) error {
	for _, attachment := range c.Snapshot().CategoryRoots() {
		if attachment.RootFolderID != rootFolderID {
			continue
		}
		attachment.AcceptsNew = acceptsNew
		if err := c.AttachRootFolder(ctx, attachment); err != nil {
			return fmt.Errorf("settings: set accepts_new for root folder %d, category %d: %w",
				rootFolderID, attachment.CategoryID, err)
		}
	}
	return nil
}

// SetRootFolderDrainState records ADR-51's own drain state machine.
// drainJobID is library.db's move_job id (no FK, cross-db), nil once a
// drain finishes, is cancelled, or never started (DrainStateNone).
func (c *Cache) SetRootFolderDrainState(ctx context.Context, id int64, state string, drainJobID *int64) error {
	if _, found := c.Snapshot().RootFolder(id); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, id)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderDrainState(ctx, settingsdb.SetRootFolderDrainStateParams{
			DrainState: state, DrainJobID: int64PtrToNull(drainJobID), ID: id,
		})
	})
}

// PlacementAffinity answers what an affinity key last resolved to (section
// 11.7's COMMIT step) -- placement.Resolve is pure, so this lookup and
// SetPlacementAffinity below are what a caller runs around it.
func (c *Cache) PlacementAffinity(ctx context.Context, categoryID int64, affinityKey string) (int64, bool, error) {
	rootFolderID, err := c.queries.GetPlacementAffinity(ctx, settingsdb.GetPlacementAffinityParams{
		CategoryID: categoryID, AffinityKey: affinityKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("settings: placement affinity %q: %w", affinityKey, err)
	}
	return rootFolderID, true, nil
}

// SetPlacementAffinity commits Resolve's choice for an affinity key, so the
// next item sharing it lands on the same root.
func (c *Cache) SetPlacementAffinity(ctx context.Context, categoryID int64, affinityKey string, rootFolderID, updatedAt int64) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetPlacementAffinity(ctx, settingsdb.SetPlacementAffinityParams{
			CategoryID: categoryID, AffinityKey: affinityKey, RootFolderID: rootFolderID, UpdatedAt: updatedAt,
		})
	})
}

// DeleteRootFolder removes a root folder, or refuses and says which categories
// still point at it.
func (c *Cache) DeleteRootFolder(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().RootFolder(id); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, id)
	}

	categories, err := c.queries.CountCategoriesUsingRootFolder(ctx, id)
	if err != nil {
		return fmt.Errorf("settings: count categories for root folder %d: %w", id, err)
	}
	if categories > 0 {
		return &InUseError{Entity: "root folder", ID: id,
			References: []Reference{{Kind: "categories", Count: categories}}}
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteRootFolder(ctx, id)
	})
}

// SetRootFolderOnline declares a root online or offline (section 10.4): the
// user's own durable call, never overwritten by a probe finding a declared-
// offline disk back (Invariant 87 -- "never auto-cleared"). An offline root
// is unavailable, never free space (Invariant 20a).
func (c *Cache) SetRootFolderOnline(ctx context.Context, id int64, online bool) error {
	if _, found := c.Snapshot().RootFolder(id); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, id)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderOnline(ctx, settingsdb.SetRootFolderOnlineParams{
			Online: boolToInt(online), ID: id,
		})
	})
}

// RecordRootFolderReach records whether a probe reached a root: an online root it cannot reach goes offline as
// Solarr's own declaration, and only such a root comes back online once reached (user, 2026-10-23). A root a person
// declared offline is left alone (Invariant 87). entries is what a reached root holds; only whether it held any is
// kept, so a new folder in it is not a settings change (each of which takes a backup).
func (c *Cache) RecordRootFolderReach(ctx context.Context, id int64, reached bool, reason string, entries int64) error {
	root, found := c.Snapshot().RootFolder(id)
	if !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, id)
	}
	if !root.Online && !root.OfflineAuto {
		return nil
	}
	online, auto, why, seen := false, true, reason, root.SeenEntries
	if reached {
		online, auto, why, seen = true, false, "", min(entries, 1)
	}
	if online == root.Online && auto == root.OfflineAuto && why == root.OfflineReason && seen == root.SeenEntries {
		return nil
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetRootFolderReachability(ctx, settingsdb.SetRootFolderReachabilityParams{
			Online: boolToInt(online), OfflineAuto: boolToInt(auto),
			OfflineReason: sql.NullString{String: why, Valid: why != ""}, SeenEntries: seen, ID: id,
		})
	})
}

// AttachRootFolder puts a root folder into a category, or updates its ordering.
func (c *Cache) AttachRootFolder(ctx context.Context, attachment CategoryRoot) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.Category(attachment.CategoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, attachment.CategoryID)
	}
	if _, found := snapshot.RootFolder(attachment.RootFolderID); !found {
		return fmt.Errorf("%w: root folder %d", ErrNotFound, attachment.RootFolderID)
	}
	// A root folder belongs to at most one library (user, 2026-09-30).
	for _, existing := range snapshot.CategoryRoots() {
		if existing.RootFolderID == attachment.RootFolderID && existing.CategoryID != attachment.CategoryID {
			return fmt.Errorf("%w: root folder %d already belongs to library %d; remove it from there first",
				ErrInUse, attachment.RootFolderID, existing.CategoryID)
		}
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.AttachRootFolderToCategory(ctx, settingsdb.AttachRootFolderToCategoryParams{
			CategoryID:   attachment.CategoryID,
			RootFolderID: attachment.RootFolderID,
			Ord:          attachment.Order,
			Weight:       attachment.Weight,
			AcceptsNew:   boolToInt(attachment.AcceptsNew),
		})
	})
}

// DetachRootFolder removes a root folder from a category.
func (c *Cache) DetachRootFolder(ctx context.Context, categoryID, rootFolderID int64) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DetachRootFolderFromCategory(ctx,
			settingsdb.DetachRootFolderFromCategoryParams{
				CategoryID: categoryID, RootFolderID: rootFolderID,
			})
	})
}

// CreateUser adds an account.
// SetPassword replaces an account's password hash. An empty hash leaves the
// account unable to sign in, which is what an open-access install stores.
func (c *Cache) SetPassword(ctx context.Context, userID int64, passwordHash string) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetUserPasswordHash(ctx, settingsdb.SetUserPasswordHashParams{
			ID:           userID,
			PasswordHash: sql.NullString{String: passwordHash, Valid: passwordHash != ""},
		})
	})
}

// CreateUser adds an account with the default limits and no grants beyond an administrator's.
func (c *Cache) CreateUser(ctx context.Context, cl clock.Clock, username, passwordHash string, role Role) (User, error) {
	return c.CreateAccount(ctx, cl, NewAccount{
		Username: username, PasswordHash: passwordHash, Role: role, Limits: c.DefaultLimits(role),
	})
}

// DeleteUser removes an account.
func (c *Cache) DeleteUser(ctx context.Context, id int64) error {
	user, found := c.Snapshot().User(id)
	if !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	if err := c.wouldStrandTheInstance(user, RoleReadonly); err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteUser(ctx, id)
	})
}

// SetRole changes what a user may do outside any one category.
func (c *Cache) SetRole(ctx context.Context, id int64, role Role) error {
	user, found := c.Snapshot().User(id)
	if !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	if err := c.wouldStrandTheInstance(user, role); err != nil {
		return err
	}
	categories := c.Snapshot().Categories()
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		if err := queries.SetUserRole(ctx, settingsdb.SetUserRoleParams{ID: id, Role: string(role)}); err != nil {
			return err
		}
		if role != RoleAdmin || user.Role == RoleAdmin {
			return nil
		}
		// A promoted administrator manages every library, as one created as administrator does.
		for _, category := range categories {
			if err := queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
				UserID: id, CategoryID: category.ID, Permission: string(PermissionManage),
			}); err != nil {
				return err
			}
		}
		return queries.SetUserLimits(ctx, settingsdb.SetUserLimitsParams{
			ID: id, UiTierDefault: string(user.Limits.TierDefault), UiTierMax: string(user.Limits.TierMax),
			NewLibraryPermission: string(PermissionManage),
		})
	})
}

// SetUserTier changes how much of the settings surface a user is shown
// (ADR-26). Self-service, unlike SetRole: raising or lowering it never
// changes behaviour, only what is displayed, so there is nothing here for
// wouldStrandTheInstance's own reasoning to protect against -- the caller
// (a user changing their own tier, PATCH /me/tier) is trusted the same way
// picking a theme would be. queries.SetUserTier itself was scaffolded ahead
// of this caller (migration 0001's own ui_tier column), the same "ready
// ahead of the consumer" shape several other todo 4.14 pieces already had.
func (c *Cache) SetUserTier(ctx context.Context, id int64, tier Tier) error {
	user, found := c.Snapshot().User(id)
	if !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, id)
	}
	if !tier.valid() {
		return config.Problems{{Path: "tier", Value: string(tier), Expectation: "normal, advanced or expert"}}
	}
	if !user.Limits.TierMax.AtLeast(tier) {
		return config.Problems{{Path: "tier", Value: string(tier),
			Expectation: fmt.Sprintf("at most %s, the level an administrator allows this account", user.Limits.TierMax)}}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetUserTier(ctx, settingsdb.SetUserTierParams{ID: id, UiTier: string(tier)})
	})
}

// RevokeCategoryAccess removes a grant entirely. An absent row denies, so this
// is not the same as storing "none" -- it returns the user to the default.
func (c *Cache) RevokeCategoryAccess(ctx context.Context, userID, categoryID int64) error {
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.RevokeCategoryAccess(ctx, settingsdb.RevokeCategoryAccessParams{
			UserID: userID, CategoryID: categoryID,
		})
	})
}

// ErrLastAdministrator reports a change that would leave nobody able to
// administer the instance.
var ErrLastAdministrator = errors.New("settings: that would leave no administrator")

// wouldStrandTheInstance refuses removing or demoting the only administrator.
// Nothing else can restore the role, so the interface would be locked out of its
// own settings with no way back that does not involve the database file
// (ADR-44).
func (c *Cache) wouldStrandTheInstance(user User, becoming Role) error {
	if user.Role != RoleAdmin || becoming == RoleAdmin {
		return nil
	}
	if len(c.administratorIDs()) > 1 {
		return nil
	}
	return fmt.Errorf("%w: %q is the only one", ErrLastAdministrator, user.Username)
}

// SetCategoryAccess grants or changes a user's permission on one category.
func (c *Cache) SetCategoryAccess(ctx context.Context, userID, categoryID int64, permission Permission) error {
	snapshot := c.Snapshot()
	if _, found := snapshot.User(userID); !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, userID)
	}
	if _, found := snapshot.Category(categoryID); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, categoryID)
	}

	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryAccess(ctx, settingsdb.SetCategoryAccessParams{
			UserID: userID, CategoryID: categoryID, Permission: string(permission),
		})
	})
}

// PasswordHash reads a user's stored hash.
func (c *Cache) PasswordHash(ctx context.Context, username string) (string, error) {
	row, err := c.queries.GetUserByUsername(ctx, username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: user %q", ErrNotFound, username)
	}
	if err != nil {
		return "", fmt.Errorf("settings: read password hash: %w", err)
	}
	return row.PasswordHash.String, nil
}

// CreateExclusionPattern adds an unwanted-file pattern (ADR-53 part 1).
// Builtin is always false here -- only the shipped migration defaults are
// ever marked builtin, so a pattern the user adds is never mistaken for one.
func (c *Cache) CreateExclusionPattern(ctx context.Context, pattern ExclusionPattern) (ExclusionPattern, error) {
	if problems := validateExclusionPattern(pattern.Pattern, pattern.Action, pattern.MediaType,
		pattern.CategoryID, c.categoryExists); len(problems) > 0 {
		return ExclusionPattern{}, problems
	}

	var created settingsdb.ExclusionPattern
	err := c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		row, err := queries.CreateExclusionPattern(ctx, settingsdb.CreateExclusionPatternParams{
			Pattern: pattern.Pattern, Action: pattern.Action, MediaType: sql.NullString{String: pattern.MediaType, Valid: pattern.MediaType != ""},
			CategoryID: int64PtrToNull(pattern.CategoryID), Note: pattern.Note,
			Builtin: 0, Enabled: boolToInt(pattern.Enabled),
		})
		created = row
		return err
	})
	if err != nil {
		return ExclusionPattern{}, fmt.Errorf("settings: create exclusion pattern %q: %w", pattern.Pattern, err)
	}
	return convertExclusionPatterns([]settingsdb.ExclusionPattern{created})[0], nil
}

// UpdateExclusionPattern replaces a pattern's content -- Builtin never
// changes after creation, only what a builtin pattern says and whether it is
// enabled (still editable and removable, per ADR-53's own text).
func (c *Cache) UpdateExclusionPattern(ctx context.Context, id int64, pattern ExclusionPattern) error {
	if _, found := c.Snapshot().ExclusionPattern(id); !found {
		return fmt.Errorf("%w: exclusion pattern %d", ErrNotFound, id)
	}
	if problems := validateExclusionPattern(pattern.Pattern, pattern.Action, pattern.MediaType,
		pattern.CategoryID, c.categoryExists); len(problems) > 0 {
		return problems
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.UpdateExclusionPattern(ctx, settingsdb.UpdateExclusionPatternParams{
			Pattern: pattern.Pattern, Action: pattern.Action, MediaType: sql.NullString{String: pattern.MediaType, Valid: pattern.MediaType != ""},
			CategoryID: int64PtrToNull(pattern.CategoryID), Note: pattern.Note,
			Enabled: boolToInt(pattern.Enabled), ID: id,
		})
	})
}

// DeleteExclusionPattern removes a pattern outright, builtin or not -- ADR-53
// is explicit that a shipped default stays "editable and removable", never
// protected from deletion the way an in-use quality profile is.
func (c *Cache) DeleteExclusionPattern(ctx context.Context, id int64) error {
	if _, found := c.Snapshot().ExclusionPattern(id); !found {
		return fmt.Errorf("%w: exclusion pattern %d", ErrNotFound, id)
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.DeleteExclusionPattern(ctx, id)
	})
}

// categoryExists adapts Snapshot().Category for validateExclusionPattern's
// own injected lookup, so the validator stays a pure function of its
// arguments rather than reaching into Cache state itself.
func (c *Cache) categoryExists(id int64) bool {
	_, found := c.Snapshot().Category(id)
	return found
}

func boolToInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// administratorIDs lists the administrator accounts.
func (c *Cache) administratorIDs() []int64 {
	var ids []int64
	for _, user := range c.Snapshot().Users() {
		if user.Role == RoleAdmin {
			ids = append(ids, user.ID)
		}
	}
	return ids
}

// UserPreferences reads one user's stored interface state -- sort order,
// chosen columns and the like (migration 0001's own user_preference table,
// unused until 2026-09-19). Read straight from the database rather than the
// snapshot: preferences scale with users times screens, and nothing outside
// the one request that asked for them ever needs them in memory.
func (c *Cache) UserPreferences(ctx context.Context, userID int64) (map[string]string, error) {
	rows, err := c.queries.ListPreferencesForUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("settings: preferences for user %d: %w", userID, err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.Key] = row.ValueJson
	}
	return out, nil
}

// SetUserPreference stores one interface preference. Self-service, like
// SetUserTier: nothing here changes behaviour, only what the caller sees.
func (c *Cache) SetUserPreference(ctx context.Context, userID int64, key, valueJSON string) error {
	if _, found := c.Snapshot().User(userID); !found {
		return fmt.Errorf("%w: user %d", ErrNotFound, userID)
	}
	if strings.TrimSpace(key) == "" {
		return config.Problems{{Path: "key", Expectation: "a preference key"}}
	}
	if !json.Valid([]byte(valueJSON)) {
		return config.Problems{{Path: "value", Value: valueJSON, Expectation: "a JSON value"}}
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetPreference(ctx, settingsdb.SetPreferenceParams{
			UserID: userID, Key: key, ValueJson: valueJSON,
		})
	})
}

// SetCategoryMonitorReleaseTypes chooses which release types a monitored
// artist in this library brings in (2026-09-19). A subscription that names
// its own types overrides this for that one artist.
func (c *Cache) SetCategoryMonitorReleaseTypes(ctx context.Context, id int64, types []string) error {
	if _, found := c.Snapshot().Category(id); !found {
		return fmt.Errorf("%w: category %d", ErrNotFound, id)
	}
	if len(types) == 0 {
		return config.Problems{{
			Path: "monitor_release_types", Expectation: "at least one release type",
		}}
	}
	encoded, err := json.Marshal(types)
	if err != nil {
		return err
	}
	return c.write(ctx, func(ctx context.Context, queries *settingsdb.Queries) error {
		return queries.SetCategoryMonitorReleaseTypes(ctx,
			settingsdb.SetCategoryMonitorReleaseTypesParams{MonitorReleaseTypes: string(encoded), ID: id})
	})
}
