package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/media/tv"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// SeriesCatalog satisfies apiserver.SeriesCatalog over the TV service and the library's series reader.
type SeriesCatalog struct {
	Service          tv.Service
	Reader           library.SeriesReader
	Desired          library.Desired
	Categories       Categories
	DefaultProfileID int64
	Clock            clock.Clock
	// Remover sends a deleted series' files to the recycle bin.
	Remover FileRemover
}

// List maps a page of series.
func (c SeriesCatalog) List(ctx context.Context, categoryID int64, after paginate.TextCursor, limit int) ([]gen.SeriesSummary, error) {
	rows, err := c.Reader.List(ctx, categoryID, library.SeriesCursor{SortTitle: after.Sort, ID: after.ID}, int64(limit), c.Clock.Now())
	if err != nil {
		return nil, err
	}
	views := make([]gen.SeriesSummary, len(rows))
	for i, row := range rows {
		views[i] = summaryView(row)
	}
	return views, nil
}

// Get maps one series with its seasons and episodes.
func (c SeriesCatalog) Get(ctx context.Context, categoryID, seriesID int64) (gen.SeriesDetail, error) {
	now := c.Clock.Now()
	row, err := c.Reader.Get(ctx, categoryID, seriesID, now)
	if err != nil {
		return gen.SeriesDetail{}, translateSeriesError(err)
	}
	detail := gen.SeriesDetail{Summary: summaryView(row.Summary), Ids: row.IDs, Seasons: []gen.SeriesSeason{}, Episodes: make([]gen.SeriesEpisode, len(row.Episodes))}
	if len(row.Aliases) > 0 {
		detail.Aliases = &row.Aliases
	}
	if row.RuntimeMs > 0 {
		minutes := int(row.RuntimeMs / 60_000)
		detail.RuntimeMinutes = &minutes
	}
	setString(&detail.LogoUrl, imageOfKind(row.Summary.Fields["images"], "logo"))
	setString(&detail.FanartUrl, imageOfKind(row.Summary.Fields["images"], "fanart"))
	if network, found, err := c.Reader.NetworkOfSeries(ctx, seriesID); err == nil && found {
		detail.NetworkId = &network.ID
		setString(&detail.NetworkLogoUrl, network.LogoURL)
	}
	setString(&detail.OriginalLanguage, row.Language)
	setString(&detail.OriginalCountry, row.Summary.Fields["original_country"])
	setString(&detail.AirTime, row.Summary.Fields["air_time"])
	posters := seasonPosters(row.Summary.Fields["season_images"])
	for _, season := range row.Summary.Seasons {
		view := gen.SeriesSeason{Number: season.Number, Monitored: season.Monitored, Counts: countsView(season.Counts), DesiredItemId: &season.DesiredItemID}
		if url := posters[season.Number]; url != "" {
			view.PosterUrl = &url
		}
		detail.Seasons = append(detail.Seasons, view)
	}
	category, haveCategory := c.Categories.Category(categoryID)
	for i, episode := range row.Episodes {
		detail.Episodes[i] = episodeView(episode, now)
		if episode.File != nil {
			detail.Episodes[i].File = episodeFileView(*episode.File, category, haveCategory)
		}
	}
	credits := make([]gen.SeriesCredit, len(row.Credits))
	for i, credit := range row.Credits {
		credits[i] = gen.SeriesCredit{PersonId: credit.PersonID, Name: credit.Name, Role: credit.Role, Order: int(credit.Order)}
		setString(&credits[i].ImageUrl, credit.ImageURL)
		setString(&credits[i].Character, credit.Character)
		if credit.PartID > 0 {
			credits[i].EpisodeId = &credit.PartID
		}
	}
	detail.Credits = &credits
	return detail, nil
}

func episodeView(episode library.SeriesEpisode, now time.Time) gen.SeriesEpisode {
	view := gen.SeriesEpisode{Id: episode.PartID, Season: episode.Season, Number: episode.Number, Monitored: episode.Monitored}
	setString(&view.Title, episode.Title)
	setString(&view.AirDate, episode.Date)
	if episode.AvailableAt > 0 {
		at := time.Unix(episode.AvailableAt, 0).UTC()
		view.AirTime = &at
	}
	if episode.AvailableConfidence != "" {
		confidence := gen.SeriesEpisodeAirConfidence(episode.AvailableConfidence)
		view.AirConfidence = &confidence
	}
	if episode.RuntimeMs > 0 {
		minutes := int(episode.RuntimeMs / 60_000)
		view.RuntimeMinutes = &minutes
	}
	switch {
	case episode.Present:
		view.State = gen.SeriesEpisodeStatePresent
	case !episode.Monitored:
		view.State = gen.SeriesEpisodeStateUnmonitored
	case episode.AvailableAt > now.Unix() || (episode.AvailableAt == 0 && episode.Date > now.UTC().Format("2006-01-02")):
		view.State = gen.SeriesEpisodeStateUnaired
	default:
		view.State = gen.SeriesEpisodeStateMissing
	}
	return view
}

// Lookup searches the sources and marks what the library already holds.
func (c SeriesCatalog) Lookup(ctx context.Context, _ int64, query string, limit int) ([]gen.SeriesCandidate, error) {
	found, err := c.Service.Search(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
	}
	views := make([]gen.SeriesCandidate, len(found))
	for i, candidate := range found {
		view := gen.SeriesCandidate{Provider: tv.ProviderTVDB, ExternalId: strconv.FormatInt(candidate.TVDBID, 10), Title: candidate.Title}
		if candidate.TVDBID == 0 {
			view.Provider, view.ExternalId = tv.ProviderTMDB, strconv.FormatInt(candidate.TMDBID, 10)
		}
		if candidate.Year > 0 {
			year := int64(candidate.Year)
			view.Year = &year
		}
		setString(&view.Network, candidate.Network)
		setString(&view.Overview, candidate.Overview)
		setString(&view.PosterUrl, candidate.Poster)
		if candidate.Status != "" {
			status := gen.SeriesCandidateStatus(candidate.Status)
			view.Status = &status
		}
		if id, held, err := c.Service.Series.WorkByProviderID(ctx, view.Provider, view.ExternalId); err == nil && held {
			view.LibraryId = &id
		}
		views[i] = view
	}
	return views, nil
}

// Add stores a series in a library with the chosen monitoring.
func (c SeriesCatalog) Add(ctx context.Context, userID int64, request gen.AddSeriesRequest) (gen.AddSeriesResult, error) {
	category, ok := c.Categories.Category(request.CategoryId)
	if !ok {
		return gen.AddSeriesResult{}, apiserver.ErrSeriesNotFound
	}
	provider := tv.ProviderTVDB
	if request.Provider != nil {
		provider = string(*request.Provider)
	}
	id, valid := tv.ParseID(request.ExternalId)
	if (provider != tv.ProviderTVDB && provider != tv.ProviderTMDB) || !valid {
		return gen.AddSeriesResult{}, fmt.Errorf("%w: only a numeric tvdb or tmdb id can be added", apiserver.ErrDiscoverUnsupportedMediaType)
	}
	ids := tv.IDs{TVDB: id}
	if provider == tv.ProviderTMDB {
		ids = tv.IDs{TMDB: id}
	}
	monitor := tv.MonitorAll
	if request.Monitor != nil {
		monitor = tv.MonitorOption(*request.Monitor)
	}
	profile := c.Categories.ProfileFor(category, c.DefaultProfileID)
	if request.QualityProfileId != nil {
		profile = *request.QualityProfileId
	}
	if err := c.RemoveExclusion(ctx, provider, request.ExternalId); err != nil {
		return gen.AddSeriesResult{}, err
	}
	result, err := c.Service.Add(ctx, tv.AddRequest{
		IDs: ids, CategoryID: request.CategoryId, ProfileID: profile, Monitor: monitor,
		Specials: request.MonitorSpecials != nil && *request.MonitorSpecials, AddedBy: &userID,
	})
	if err != nil {
		if errors.Is(err, tv.ErrNoSource) {
			return gen.AddSeriesResult{}, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
		}
		return gen.AddSeriesResult{}, err
	}
	return gen.AddSeriesResult{SeriesId: result.WorkID, Created: result.Created, Episodes: result.Episodes, Seasons: result.Seasons}, nil
}

// Refresh re-reads one series from the sources; monitoring and season items stay as they are.
func (c SeriesCatalog) Refresh(ctx context.Context, categoryID, seriesID int64) (gen.AddSeriesResult, error) {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return gen.AddSeriesResult{}, translateSeriesError(err)
	}
	profile := c.DefaultProfileID
	if category, ok := c.Categories.Category(categoryID); ok {
		profile = c.Categories.ProfileFor(category, profile)
	}
	result, err := c.Service.Refresh(ctx, tv.RefreshRequest{WorkID: seriesID, CategoryID: categoryID, ProfileID: profile})
	if err != nil {
		if errors.Is(err, tv.ErrNoSource) {
			return gen.AddSeriesResult{}, fmt.Errorf("%w: %w", apiserver.ErrSeriesSourceUnavailable, err)
		}
		return gen.AddSeriesResult{}, err
	}
	return gen.AddSeriesResult{SeriesId: result.WorkID, Created: false, Episodes: result.Episodes, Seasons: result.Seasons}, nil
}

// Delete removes a series. Its files go to the recycle bin when asked (never deleted directly), and the series is
// remembered when it must not be added back; with neither, the next scan finds the folder and adds it again.
func (c SeriesCatalog) Delete(ctx context.Context, categoryID, seriesID int64, deleteFiles, exclude bool) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	summaries, err := c.Reader.SummariesByIDs(ctx, categoryID, []int64{seriesID}, c.Clock.Now())
	if err != nil || len(summaries) == 0 {
		return translateSeriesError(library.ErrSeriesNotFound)
	}
	provider, err := c.Reader.ProviderIDs(ctx, seriesID)
	if err != nil {
		return err
	}
	if deleteFiles {
		files, err := c.Reader.MediaObjects(ctx, seriesID)
		if err != nil {
			return err
		}
		recycling := RecyclingFor(c.Categories, []int64{categoryID})
		for _, mediaObjectID := range files {
			if err := c.Remover.RecycleFile(ctx, mediaObjectID, recycling); err != nil {
				return fmt.Errorf("recycle file %d: %w", mediaObjectID, err)
			}
		}
	}
	if exclude {
		var ids []library.ExternalIDWrite
		for name, id := range provider {
			ids = append(ids, library.ExternalIDWrite{Provider: name, ExternalID: id})
		}
		if err := c.Service.Series.Exclude(ctx, c.Clock.Now(), summaries[0].Title, ids); err != nil {
			return err
		}
	}
	return c.Service.Series.DeleteSeries(ctx, seriesID)
}

// Exclusions lists the deleted series that are not added again, one entry per series.
func (c SeriesCatalog) Exclusions(ctx context.Context) ([]gen.SeriesExclusion, error) {
	rows, err := c.Service.Series.Exclusions(ctx)
	if err != nil {
		return nil, err
	}
	type key struct {
		title string
		at    int64
	}
	seen := map[key]bool{}
	views := make([]gen.SeriesExclusion, 0, len(rows))
	for _, row := range rows {
		if k := (key{row.Title, row.CreatedAt}); !seen[k] {
			seen[k] = true
			views = append(views, gen.SeriesExclusion{Title: row.Title, Provider: row.Provider, ExternalId: row.ExternalID, CreatedAt: row.CreatedAt})
		}
	}
	return views, nil
}

// RemoveExclusion allows the series the id belongs to again, by every id it was excluded by.
func (c SeriesCatalog) RemoveExclusion(ctx context.Context, provider, externalID string) error {
	rows, err := c.Service.Series.Exclusions(ctx)
	if err != nil {
		return err
	}
	var ids []library.ExternalIDWrite
	for _, row := range rows {
		if row.Provider == provider && row.ExternalID == externalID {
			for _, sibling := range rows {
				if sibling.Title == row.Title && sibling.CreatedAt == row.CreatedAt {
					ids = append(ids, library.ExternalIDWrite{Provider: sibling.Provider, ExternalID: sibling.ExternalID})
				}
			}
		}
	}
	return c.Service.Series.Unexclude(ctx, ids)
}

// ApplyMonitor re-applies one of Sonarr's monitor options to a series the library holds.
func (c SeriesCatalog) ApplyMonitor(ctx context.Context, categoryID, seriesID int64, option string, specials bool) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	if !tv.MonitorOption(option).Valid() {
		return fmt.Errorf("%w: unknown monitor option %q", apiserver.ErrInvalidChoice, option)
	}
	return c.Service.ApplyMonitor(ctx, categoryID, seriesID, tv.MonitorOption(option), specials)
}

// SetType says what kind of series this is; "auto" returns to what the providers say.
func (c SeriesCatalog) SetType(ctx context.Context, categoryID, seriesID int64, seriesType string) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	value := seriesType
	if seriesType == "auto" {
		value = ""
	} else if !tv.SeriesType(seriesType).Valid() {
		return fmt.Errorf("%w: unknown series type %q", apiserver.ErrInvalidChoice, seriesType)
	}
	return c.Service.Series.SetManualField(ctx, c.Clock.Now(), seriesID, "series_type", value)
}

// SetProfile gives the series a quality profile of its own, or the library's when nil.
func (c SeriesCatalog) SetProfile(ctx context.Context, categoryID, seriesID int64, profileID *int64) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	if profileID != nil {
		profile, found := c.Categories.Cache.Snapshot().QualityProfile(*profileID)
		if !found || profile.MediaType != "tv" {
			return fmt.Errorf("%w: quality profile %d is not a TV profile", apiserver.ErrInvalidChoice, *profileID)
		}
	}
	return c.Service.SetProfile(ctx, categoryID, seriesID, profileID)
}

// SetEpisodeMonitored monitors or unmonitors one episode of a series in the library.
func (c SeriesCatalog) SetEpisodeMonitored(ctx context.Context, categoryID, seriesID, partID int64, monitored bool) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	err := c.Desired.SetPartMonitored(ctx, seriesID, partID, monitored)
	if errors.Is(err, library.ErrPartNotFound) {
		return apiserver.ErrSeriesNotFound
	}
	return err
}

// SetSeasonMonitored monitors or unmonitors a whole season.
func (c SeriesCatalog) SetSeasonMonitored(ctx context.Context, categoryID, seriesID int64, season int, monitored bool) error {
	if err := c.Reader.InCategory(ctx, categoryID, seriesID); err != nil {
		return translateSeriesError(err)
	}
	found, err := c.Desired.SetSeasonMonitored(ctx, seriesID, tv.SeasonRef(season), monitored)
	if err != nil {
		return err
	}
	if !found {
		return apiserver.ErrSeriesNotFound
	}
	return nil
}

func translateSeriesError(err error) error {
	if errors.Is(err, library.ErrSeriesNotFound) {
		return apiserver.ErrSeriesNotFound
	}
	return err
}

func summaryView(row library.SeriesSummary) gen.SeriesSummary {
	view := gen.SeriesSummary{
		Id: row.WorkID, Title: row.Title, SortTitle: row.SortTitle, Monitored: row.Monitored, Counts: countsView(row.Counts), AddedAt: row.AddedAt,
	}
	states := make([]struct {
		Monitored bool `json:"monitored"`
		Number    int  `json:"number"`
	}, 0, len(row.Seasons))
	for _, season := range row.Seasons {
		if season.Number > 0 {
			view.Seasons++
		}
		states = append(states, struct {
			Monitored bool `json:"monitored"`
			Number    int  `json:"number"`
		}{Monitored: season.Monitored, Number: season.Number})
	}
	view.SeasonStates = &states
	if row.Year > 0 {
		view.Year = &row.Year
	}
	if status := row.Fields["status"]; status != "" {
		typed := gen.SeriesSummaryStatus(status)
		view.Status = &typed
	}
	setString(&view.Network, row.Fields["network"])
	if kind := tv.SeriesType(row.Fields["series_type"]); kind.Valid() {
		typed := gen.SeriesSummarySeriesType(kind)
		view.SeriesType = &typed
	}
	setString(&view.Overview, row.Description)
	setString(&view.PosterUrl, posterOf(row.Fields["images"]))
	if row.NextAvailable > 0 {
		next := time.Unix(row.NextAvailable, 0).UTC()
		view.NextAiring = &next
	}
	if row.ProfileID > 0 {
		view.QualityProfileId = &row.ProfileID
	}
	if len(row.Genres) > 0 {
		view.Genres = &row.Genres
	}
	return view
}

func countsView(counts library.SeriesCounts) gen.SeriesCounts {
	return gen.SeriesCounts{Total: int(counts.Total), Monitored: int(counts.Monitored), Present: int(counts.Present), AiredMissing: int(counts.AiredMissing)}
}

func posterOf(imagesJSON string) string { return imageOfKind(imagesJSON, "poster") }

// imageOfKind is the first picture of a kind (poster, fanart, logo) in a series' stored images, in the provider order.
func imageOfKind(imagesJSON, kind string) string {
	var images []tv.Image
	if json.Unmarshal([]byte(imagesJSON), &images) != nil {
		return ""
	}
	for _, image := range images {
		if image.Kind == kind {
			return image.URL
		}
	}
	return ""
}

func seasonPosters(seasonsJSON string) map[int]string {
	var seasons []tv.Season
	posters := map[int]string{}
	if json.Unmarshal([]byte(seasonsJSON), &seasons) == nil {
		for _, season := range seasons {
			posters[season.Number] = season.Poster
		}
	}
	return posters
}

func setString(into **string, value string) {
	if value != "" {
		*into = &value
	}
}

// Wanted lists the library's missing episodes.
func (c SeriesCatalog) Wanted(ctx context.Context, categoryID int64, after paginate.TextCursor, limit int) ([]gen.WantedEpisode, error) {
	rows, err := c.Reader.Wanted(ctx, categoryID, library.WantedCursor{Date: after.Sort, ID: after.ID}, int64(limit), c.Clock.Now())
	if err != nil {
		return nil, err
	}
	views := make([]gen.WantedEpisode, len(rows))
	for i, row := range rows {
		view := gen.WantedEpisode{PartId: row.PartID, SeriesId: row.WorkID, SeriesTitle: row.SeriesTitle, Season: row.Season, Number: row.Number}
		setString(&view.Title, row.Title)
		setString(&view.AirDate, row.Date)
		if row.AvailableAt > 0 {
			at := time.Unix(row.AvailableAt, 0).UTC()
			view.AirTime = &at
		}
		views[i] = view
	}
	return views, nil
}

func episodeFileView(file library.EpisodeFile, category settings.Category, haveCategory bool) *gen.SeriesEpisodeFile {
	view := &gen.SeriesEpisodeFile{MediaObjectId: file.MediaObjectID, SizeBytes: file.SizeBytes, Subtitles: file.Subtitles}
	if view.Subtitles == nil {
		view.Subtitles = []string{}
	}
	setString(&view.Resolution, file.Resolution)
	setString(&view.VideoCodec, file.VideoCodec)
	setString(&view.Hdr, file.HDR)
	setString(&view.AudioCodec, file.AudioCodec)
	setString(&view.AudioChannels, file.AudioChannels)
	setString(&view.Container, file.Container)
	if file.VideoBitrate > 0 {
		kbps := int(file.VideoBitrate / 1000)
		view.VideoBitrateKbps = &kbps
	}
	if haveCategory && category.QualityTarget != nil && file.VideoCodec != "" {
		meets := quality.MeetsTarget(*category.QualityTarget, score.FileFacts{
			Resolution: file.Resolution, VideoCodec: file.VideoCodec, HDR: file.HDR, AudioCodec: file.AudioCodec, Container: file.Container,
		})
		view.MeetsTarget = &meets
	}
	return view
}
