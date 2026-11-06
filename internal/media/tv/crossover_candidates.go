package tv

import (
	"cmp"
	"context"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/metadata/wikidata"
)

const (
	// crossoverFamilyMax keeps a whole universe from being compared episode by episode.
	crossoverFamilyMax = 14
	// crossoverFetchBudget bounds the series lists one pass reads from the providers.
	crossoverFetchBudget = 40
	// crossoverRecheck is how long a family compared once is left alone.
	crossoverRecheck = 30 * 24 * time.Hour
	// eventSpan is how far apart the first and last episode of one event may have aired.
	eventSpan = 60 * 24 * time.Hour
	// namedWindow is how far apart two episodes that name each other's series may have aired.
	namedWindow = 7 * 24 * time.Hour
	// pilotWindow is how long before a spin-off's first episode its parent may have introduced it.
	pilotWindow = 14 * 24 * time.Hour
	// eventEpisodesMax keeps a shared episode title from becoming an event.
	eventEpisodesMax = 12
	// namedMin is the shortest series title that can be found inside an episode title.
	namedMin = 6
)

// EpisodeLists gives a series' episodes as the providers list them, held by the library or not.
type EpisodeLists interface {
	Fetch(ctx context.Context, ids IDs) (Series, error)
}

// listedSeries is a series of a family with the episodes the providers list for it.
type listedSeries struct {
	provider, externalID, title string
	held                        bool
	episodes                    []Episode
}

type listedEpisode struct {
	series *listedSeries
	Episode
	base   string
	number int
	at     time.Time
}

func (e listedEpisode) key() string {
	return e.series.provider + ":" + e.series.externalID + ":" + strconv.Itoa(e.Season) + ":" + strconv.Itoa(e.Number)
}

// datedEpisodes are a family's episodes with an air date, specials left out.
func datedEpisodes(family []listedSeries) []listedEpisode {
	var out []listedEpisode
	for i := range family {
		for _, episode := range family[i].episodes {
			at, err := time.Parse("2006-01-02", episode.AirDate)
			if err != nil || episode.Special || episode.Season == 0 || strings.TrimSpace(episode.Title) == "" {
				continue
			}
			base, number := library.EventTitle(episode.Title)
			out = append(out, listedEpisode{series: &family[i], Episode: episode, base: base, number: number, at: at})
		}
	}
	slices.SortFunc(out, func(a, b listedEpisode) int { return cmp.Or(a.at.Compare(b.at), strings.Compare(a.key(), b.key())) })
	return out
}

func (e listedEpisode) part(number int) library.ProviderEventPart {
	return library.ProviderEventPart{
		Number: number, Provider: e.series.provider, ExternalID: e.series.externalID, SeriesTitle: e.series.title,
		Season: e.Season, Episode: e.Number, Title: e.Title, Date: e.AirDate,
	}
}

// findCrossoverEvents answers the events among a family's episodes: one title in several series within weeks, an episode
// named for another series and aired near it, and a spin-off's introducing episode; a lone pair both held is left to suggestions.
func findCrossoverEvents(family []listedSeries) []library.ProviderEvent {
	episodes := datedEpisodes(family)
	used := map[string]bool{}
	var events []library.ProviderEvent
	byTitle := map[string][]listedEpisode{}
	for _, episode := range episodes {
		if len(episode.base) >= 6 && !library.IsGenericEpisodeTitle(episode.base) {
			byTitle[strings.ToLower(episode.base)] = append(byTitle[strings.ToLower(episode.base)], episode)
		}
	}
	keys := make([]string, 0, len(byTitle))
	for key := range byTitle {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		for _, cluster := range clusterByWeeks(byTitle[key]) {
			if event, ok := sharedTitleEvent(cluster); ok {
				events = append(events, event)
				for _, episode := range cluster {
					used[episode.key()] = true
				}
			}
		}
	}
	events = append(events, namedEvents(family, episodes, used)...)
	events = append(events, pilotEvents(episodes, used)...)
	return events
}

// clusterByWeeks splits the episodes, oldest first, into groups that each fall within one event's span.
func clusterByWeeks(episodes []listedEpisode) [][]listedEpisode {
	var clusters [][]listedEpisode
	for _, episode := range episodes {
		last := len(clusters) - 1
		if last >= 0 && episode.at.Sub(clusters[last][0].at) <= eventSpan {
			clusters[last] = append(clusters[last], episode)
		} else {
			clusters = append(clusters, []listedEpisode{episode})
		}
	}
	return clusters
}

func sharedTitleEvent(cluster []listedEpisode) (library.ProviderEvent, bool) {
	series, days, marked := map[*listedSeries]bool{}, map[string]bool{}, false
	allHeld := true
	for _, episode := range cluster {
		series[episode.series] = true
		days[episode.AirDate] = true
		marked = marked || episode.number != 0
		allHeld = allHeld && episode.series.held
	}
	if len(series) < 2 || len(cluster) > eventEpisodesMax {
		return library.ProviderEvent{}, false
	}
	// Two unmarked episodes of one title are one broadcast listed twice; the library suggests those when it holds both.
	if !marked && (len(cluster) < 3 || len(days) < 2) && allHeld {
		return library.ProviderEvent{}, false
	}
	if !marked && len(cluster) < 2 {
		return library.ProviderEvent{}, false
	}
	event := library.ProviderEvent{Name: cluster[0].base}
	for _, episode := range cluster {
		event.Parts = append(event.Parts, episode.part(episode.number))
	}
	return event, true
}

// normalised is a title as plain lower-case letters and digits, to find one title inside another.
func normalised(title string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(title) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func names(text string, series *listedSeries) bool {
	title := normalised(series.title)
	return len(title) >= namedMin && strings.Contains(normalised(text), title)
}

// namedEvents finds an episode whose title names another series of the family, aired within a week of one of that
// series' own episodes: the crossover that is announced by its title.
func namedEvents(family []listedSeries, episodes []listedEpisode, used map[string]bool) []library.ProviderEvent {
	var events []library.ProviderEvent
	for _, episode := range episodes {
		if used[episode.key()] || library.IsGenericEpisodeTitle(episode.Title) {
			continue
		}
		for i := range family {
			other := &family[i]
			if other == episode.series || !names(episode.Title, other) {
				continue
			}
			for _, partner := range episodes {
				if partner.series != other || used[partner.key()] || partner.at.Sub(episode.at).Abs() > namedWindow {
					continue
				}
				if !episode.series.held && !other.held {
					continue
				}
				used[episode.key()], used[partner.key()] = true, true
				pair := []listedEpisode{episode, partner}
				slices.SortFunc(pair, func(a, b listedEpisode) int { return a.at.Compare(b.at) })
				events = append(events, library.ProviderEvent{Name: episode.base, Parts: []library.ProviderEventPart{pair[0].part(0), pair[1].part(0)}})
				break
			}
			break
		}
	}
	return events
}

// pilotEvents finds the first episode of a spin-off and the episode of its parent that introduced it: the parent's episode
// within two weeks before it that names the spin-off in its title or summary, or the pilot's summary naming the parent.
func pilotEvents(episodes []listedEpisode, used map[string]bool) []library.ProviderEvent {
	first := map[*listedSeries]listedEpisode{}
	for _, episode := range episodes {
		if _, seen := first[episode.series]; !seen {
			first[episode.series] = episode
		}
	}
	var events []library.ProviderEvent
	for child, pilot := range first {
		if used[pilot.key()] {
			continue
		}
		for _, introducer := range episodes {
			if introducer.series == child || used[introducer.key()] || !introducer.series.held && !child.held {
				continue
			}
			gap := pilot.at.Sub(introducer.at)
			if gap < -24*time.Hour || gap > pilotWindow {
				continue
			}
			if !names(introducer.Title+" "+introducer.Overview, child) && !names(pilot.Overview, introducer.series) {
				continue
			}
			used[pilot.key()], used[introducer.key()] = true, true
			name := cmp.Or(introducer.base, pilot.base)
			if library.IsGenericEpisodeTitle(name) {
				name = pilot.base
			}
			events = append(events, library.ProviderEvent{Name: name, Parts: []library.ProviderEventPart{introducer.part(0), pilot.part(0)}})
			break
		}
	}
	slices.SortFunc(events, func(a, b library.ProviderEvent) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Parts[0].Date, b.Parts[0].Date))
	})
	return events
}

// providerIDOf is the id the library keeps a series by: TheTVDB's, else TMDB's.
func providerIDOf(tvdb, tmdb int64) (provider, externalID string, ids IDs) {
	if tvdb > 0 {
		return ProviderTVDB, strconv.FormatInt(tvdb, 10), IDs{TVDB: tvdb}
	}
	return ProviderTMDB, strconv.FormatInt(tmdb, 10), IDs{TMDB: tmdb}
}

type crossoverFamily struct {
	key     string
	members []wikidata.FranchiseSeries
}

// crossoverFamilies groups the series Wikidata ties to each library series: itself, its franchise and its spin-offs.
func crossoverFamilies(known wikidataKnowledge, titles map[int64]string) []crossoverFamily {
	var out []crossoverFamily
	seen := map[string]bool{}
	workIDs := make([]int64, 0, len(known.refs))
	for workID := range known.refs {
		workIDs = append(workIDs, workID)
	}
	slices.Sort(workIDs)
	for _, workID := range workIDs {
		ref := known.refs[workID]
		members := []wikidata.FranchiseSeries{{Label: titles[workID], TVDB: ref.TVDB, TMDB: ref.TMDB}}
		members = append(members, familySeries([]wikidata.SeriesRef{ref}, nil, known.ties, known.members)...)
		var kept []wikidata.FranchiseSeries
		var keys []string
		for _, member := range members {
			provider, externalID, _ := providerIDOf(member.TVDB, member.TMDB)
			if member.TVDB == 0 && member.TMDB == 0 || slices.Contains(keys, provider+":"+externalID) {
				continue
			}
			keys = append(keys, provider+":"+externalID)
			kept = append(kept, member)
		}
		slices.Sort(keys)
		key := strings.Join(keys, ",")
		if len(kept) < 2 || len(kept) > crossoverFamilyMax || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, crossoverFamily{key: key, members: kept})
	}
	return out
}

// findProviderCrossovers compares the providers' episode lists of each family of series Wikidata ties together, those
// the library lacks included, so the events found list their missing parts. A family is compared once a month.
func (p FranchisePass) findProviderCrossovers(ctx context.Context, known *wikidataKnowledge) error {
	if p.Episodes == nil || p.Clock == nil || known == nil {
		return nil
	}
	works, err := p.Franchises.WorkNames(ctx, MediaType)
	if err != nil {
		return err
	}
	titles := map[int64]string{}
	for _, work := range works {
		titles[work.WorkID] = work.Title
	}
	series, err := p.Franchises.SeriesIDs(ctx)
	if err != nil {
		return err
	}
	held := heldIndex(series)
	fetched, recorded := 0, 0
	for _, family := range crossoverFamilies(*known, titles) {
		if checked, ok, err := p.Crossovers.FamilyCheckedAt(ctx, family.key); err != nil {
			return err
		} else if ok && p.Clock.Now().Sub(checked) < crossoverRecheck {
			continue
		}
		if fetched+len(family.members) > crossoverFetchBudget {
			break
		}
		fetched += len(family.members)
		listed, complete := p.listFamily(ctx, family, held)
		for _, event := range findCrossoverEvents(listed) {
			done, err := p.Crossovers.RecordProviderEvent(ctx, MediaType, event)
			if err != nil {
				return err
			}
			if done {
				recorded++
			}
		}
		if complete {
			if err := p.Crossovers.MarkFamilyChecked(ctx, family.key); err != nil {
				return err
			}
		}
	}
	p.Logger.Info("provider crossovers compared", slog.Int("series read", fetched), slog.Int("events kept", recorded))
	return nil
}

// listFamily reads the episode list of each series of the family; complete is false when a read failed, so the family is tried again.
func (p FranchisePass) listFamily(ctx context.Context, family crossoverFamily, held heldSeries) ([]listedSeries, bool) {
	complete := true
	listed := make([]listedSeries, 0, len(family.members))
	for _, member := range family.members {
		provider, externalID, ids := providerIDOf(member.TVDB, member.TMDB)
		_, isHeld := held.of(member)
		fetched, err := p.Episodes.Fetch(ctx, ids)
		if err != nil {
			p.Logger.Debug("episode list not read", slog.String("series", member.Label), slog.Any("error", err))
			complete = false
			continue
		}
		listed = append(listed, listedSeries{provider: provider, externalID: externalID, title: cmp.Or(fetched.Title, member.Label), held: isHeld, episodes: fetched.Episodes})
	}
	return listed, complete
}
