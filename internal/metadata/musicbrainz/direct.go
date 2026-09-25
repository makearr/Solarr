package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// DirectName is the ADR-13 provider bucket for musicbrainz.org itself -- the
// fallback ADR-29 says the proxy must be able to fail over to, never optional.
const DirectName = "musicbrainz"

// BrainzMashName is the ADR-13 provider bucket for the BrainzMash community pool.
const BrainzMashName = "brainzmash"

// direct talks to musicbrainz.org/ws/2 (ADR-29): one request per second, meant
// literally, enforced by the shared httpx.Limiter the caller configures the
// client with, not by anything in this package.
type direct struct {
	client  *httpx.Client
	baseURL string
	name    string
}

// NewDirect builds the direct source. client's Provider must be DirectName.
func NewDirect(client *httpx.Client, baseURL string) Source {
	return direct{client: client, baseURL: baseURL, name: DirectName}
}

// NewBrainzMash builds the BrainzMash source: a read-only ws/2 mirror pool, so it is
// the direct client under its own bucket. client's Provider must be BrainzMashName.
func NewBrainzMash(client *httpx.Client, baseURL string) Source {
	return direct{client: client, baseURL: baseURL, name: BrainzMashName}
}

func (d direct) Name() string { return d.name }

// An empty releaseMBID answers the release group alone (a zero Release, no
// error), the same as proxy.Album -- see its own doc comment for why.
func (d direct) Album(ctx context.Context, releaseGroupMBID, releaseMBID string) (ReleaseGroup, Release, error) {
	rg, err := d.releaseGroup(ctx, releaseGroupMBID)
	if err != nil {
		return ReleaseGroup{}, Release{}, err
	}
	if releaseMBID == "" {
		return rg, Release{}, nil
	}
	release, err := d.release(ctx, releaseMBID)
	if err != nil {
		return ReleaseGroup{}, Release{}, err
	}
	return rg, release, nil
}

// directRelation is the "relations" shape both the release-group and artist
// lookups share (inc=url-rels), used only to find the "wikidata" one.
type directRelation struct {
	Type string `json:"type"`
	URL  struct {
		Resource string `json:"resource"`
	} `json:"url"`
}

// wikidataQIDFromRelations finds the "wikidata" URL relation, if any -- the
// same extraction Artist and releaseGroup both need.
func wikidataQIDFromRelations(relations []directRelation) string {
	for _, relation := range relations {
		if relation.Type == "wikidata" {
			return wikidataQID(relation.URL.Resource)
		}
	}
	return ""
}

// directReleaseGroup is musicbrainz.org's release-group JSON with
// inc=artist-credits+genres+url-rels -- ArtistCredit, Genres and the
// WikidataQID need those incs, every other field is already in the default
// response.
type directReleaseGroup struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	PrimaryType      string `json:"primary-type"`
	FirstReleaseDate string `json:"first-release-date"`
	ArtistCredit     []struct {
		Name   string `json:"name"`
		Artist struct {
			ID string `json:"id"`
		} `json:"artist"`
	} `json:"artist-credit"`
	Genres []struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	} `json:"genres"`
	Relations []directRelation `json:"relations"`
}

func (d direct) releaseGroup(ctx context.Context, mbid string) (ReleaseGroup, error) {
	var parsed directReleaseGroup
	url := fmt.Sprintf("%s/release-group/%s?fmt=json&inc=artist-credits+genres+url-rels", d.baseURL, mbid)
	if err := getJSON(ctx, d.client, url, &parsed); err != nil {
		return ReleaseGroup{}, fmt.Errorf("musicbrainz: release-group %s: %w", mbid, err)
	}

	credits := make([]ArtistCredit, len(parsed.ArtistCredit))
	for i, credit := range parsed.ArtistCredit {
		credits[i] = ArtistCredit{MBID: credit.Artist.ID, Name: credit.Name}
	}
	genres := make([]Genre, len(parsed.Genres))
	for i, genre := range parsed.Genres {
		genres[i] = Genre{Name: genre.Name, Count: genre.Count}
	}

	return ReleaseGroup{
		MBID: parsed.ID, Title: parsed.Title,
		PrimaryType: parsed.PrimaryType, FirstReleaseDate: parsed.FirstReleaseDate,
		ArtistCredit: credits, Genres: genres,
		WikidataQID: wikidataQIDFromRelations(parsed.Relations),
	}, nil
}

// directArtist is musicbrainz.org's artist JSON with inc=url-rels -- verified
// against a real live response (Vutu Doci).
type directArtist struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SortName       string `json:"sort-name"`
	Disambiguation string `json:"disambiguation"`
	Type           string `json:"type"`
	Gender         string `json:"gender"`
	Country        string `json:"country"`
	Area           struct {
		Name string `json:"name"`
	} `json:"area"`
	BeginArea struct {
		Name string `json:"name"`
	} `json:"begin-area"`
	LifeSpan struct {
		Begin string `json:"begin"`
		End   string `json:"end"`
	} `json:"life-span"`
	Relations []directRelation `json:"relations"`
}

// Artist implements ArtistLookup: full artist detail, including the Wikidata
// QID a real biography needs (ADR-8) -- MusicBrainz's own "wikidata" URL
// relation, not a separate lookup.
func (d direct) Artist(ctx context.Context, mbid string) (Artist, error) {
	var parsed directArtist
	url := fmt.Sprintf("%s/artist/%s?fmt=json&inc=url-rels", d.baseURL, mbid)
	if err := getJSON(ctx, d.client, url, &parsed); err != nil {
		return Artist{}, fmt.Errorf("musicbrainz: artist %s: %w", mbid, err)
	}

	return Artist{
		MBID: parsed.ID, Name: parsed.Name, SortName: parsed.SortName,
		Disambiguation: parsed.Disambiguation, Type: parsed.Type, Gender: parsed.Gender,
		Country: parsed.Country, Area: parsed.Area.Name, BeginArea: parsed.BeginArea.Name,
		Born: parsed.LifeSpan.Begin, Died: parsed.LifeSpan.End,
		WikidataQID: wikidataQIDFromRelations(parsed.Relations),
	}, nil
}

// wikidataQID extracts "Q636" from "https://www.wikidata.org/wiki/Q636" --
// the only shape MusicBrainz's own wikidata relation uses.
func wikidataQID(resource string) string {
	_, qid, found := strings.Cut(resource, "/wiki/")
	if !found {
		return ""
	}
	return qid
}

// directSearchResponse is musicbrainz.org's release-group search JSON
// (`/release-group/?query=...`) -- verified against a real live response.
type directSearchResponse struct {
	ReleaseGroups []directSearchReleaseGroup `json:"release-groups"`
}

type directSearchReleaseGroup struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primary-type"`
	SecondaryTypes   []string `json:"secondary-types"`
	FirstReleaseDate string   `json:"first-release-date"`
	ArtistCredit     []struct {
		Name   string `json:"name"`
		Artist struct {
			Name string `json:"name"`
		} `json:"artist"`
	} `json:"artist-credit"`
}

// SearchReleaseGroups implements Searcher against musicbrainz.org's Lucene search
// endpoint, scoped to artist and release-group (album) title -- the two fields a
// caller with no MBID at all can actually claim to know (section 10.2).
func (d direct) SearchReleaseGroups(ctx context.Context, artist, title string, limit int) ([]ReleaseGroupMatch, error) {
	// artist is the credited name, artistname the artist's own: 2Ibe's live album is credited "Tupac".
	query := fmt.Sprintf(`(artist:%s OR artistname:%s) AND releasegroup:%s`,
		luceneQuoted(artist), luceneQuoted(artist), luceneQuoted(title))
	matches, err := d.searchReleaseGroups(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: search release-group %q/%q: %w", artist, title, err)
	}
	return matches, nil
}

// directArtistSearchResponse is ws/2's artist search JSON. The artist
// documents it returns carry the same fields as a lookup plus a score, but
// never relations, so a chosen result still needs Artist() for its Wikidata
// QID.
type directArtistSearchResponse struct {
	Artists []struct {
		directArtist
		Score int `json:"score"`
	} `json:"artists"`
}

// SearchArtists implements ArtistSearcher: MusicBrainz's Lucene search over
// the artist document, for someone adding an artist the library has never
// seen. Results come back in MusicBrainz's own relevance order, which is kept.
func (d direct) SearchArtists(ctx context.Context, query string, limit int) ([]ArtistMatch, error) {
	requestURL := fmt.Sprintf("%s/artist/?query=%s&fmt=json&limit=%d",
		d.baseURL, url.QueryEscape(query), limit)

	var parsed directArtistSearchResponse
	if err := getJSON(ctx, d.client, requestURL, &parsed); err != nil {
		return nil, fmt.Errorf("musicbrainz: search artist %q: %w", query, err)
	}

	matches := make([]ArtistMatch, len(parsed.Artists))
	for i, a := range parsed.Artists {
		matches[i] = ArtistMatch{
			Artist: Artist{
				MBID: a.ID, Name: a.Name, SortName: a.SortName,
				Disambiguation: a.Disambiguation, Type: a.Type, Gender: a.Gender,
				Country: a.Country, Area: a.Area.Name, BeginArea: a.BeginArea.Name,
				Born: a.LifeSpan.Begin, Died: a.LifeSpan.End,
			},
			Score: a.Score,
		}
	}
	return matches, nil
}

// SearchReleaseGroupsByQuery is QuerySearcher's own free-text search (todo
// 4.17): MusicBrainz's Lucene default-field search over the release group's
// own document (title, artist credit, tags, ...) rather than SearchReleaseGroups'
// two named fields, since a Discover query box has nothing to split a raw
// string into.
func (d direct) SearchReleaseGroupsByQuery(ctx context.Context, query string, limit int) ([]ReleaseGroupMatch, error) {
	matches, err := d.searchReleaseGroups(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz: search release-group %q: %w", query, err)
	}
	return matches, nil
}

// searchReleaseGroups is both search methods' shared request/parse -- only
// the Lucene query string itself differs between a fielded and a free-text
// search.
func (d direct) searchReleaseGroups(ctx context.Context, luceneQuery string, limit int) ([]ReleaseGroupMatch, error) {
	requestURL := fmt.Sprintf("%s/release-group/?query=%s&fmt=json&limit=%d",
		d.baseURL, url.QueryEscape(luceneQuery), limit)

	var parsed directSearchResponse
	if err := getJSON(ctx, d.client, requestURL, &parsed); err != nil {
		return nil, err
	}

	matches := make([]ReleaseGroupMatch, len(parsed.ReleaseGroups))
	for i, rg := range parsed.ReleaseGroups {
		var creditNames, artistNames []string
		for _, credit := range rg.ArtistCredit {
			creditNames = append(creditNames, credit.Name)
			artistNames = append(artistNames, credit.Artist.Name)
		}
		matches[i] = ReleaseGroupMatch{
			ReleaseGroup: ReleaseGroup{
				MBID: rg.ID, Title: rg.Title, PrimaryType: rg.PrimaryType,
				SecondaryTypes: rg.SecondaryTypes, FirstReleaseDate: rg.FirstReleaseDate,
			},
			ArtistCredit: strings.Join(creditNames, " & "),
			ArtistNames:  strings.Join(artistNames, " & "),
		}
	}
	return matches, nil
}

// directRecordingSearchResponse is ws/2's recording search JSON, verified live 2026-09-17.
type directRecordingSearchResponse struct {
	Recordings []struct {
		ID           string `json:"id"`
		Title        string `json:"title"`
		Length       *int64 `json:"length"`
		ArtistCredit []struct {
			Name       string `json:"name"`
			JoinPhrase string `json:"joinphrase"`
		} `json:"artist-credit"`
		Releases []struct {
			ReleaseGroup directSearchReleaseGroup `json:"release-group"`
		} `json:"releases"`
	} `json:"recordings"`
}

// SearchRecordingReleaseGroups implements RecordingSearcher; an empty artist searches the title alone.
func (d direct) SearchRecordingReleaseGroups(ctx context.Context, artist, title string, limit int) ([]RecordingMatch, error) {
	query := "recording:" + luceneQuoted(title)
	if artist != "" {
		query += " AND artist:" + luceneQuoted(artist)
	}
	requestURL := fmt.Sprintf("%s/recording/?query=%s&fmt=json&limit=%d", d.baseURL, url.QueryEscape(query), limit)

	var parsed directRecordingSearchResponse
	if err := getJSON(ctx, d.client, requestURL, &parsed); err != nil {
		return nil, fmt.Errorf("musicbrainz: search recording %q/%q: %w", artist, title, err)
	}
	matches := make([]RecordingMatch, len(parsed.Recordings))
	for i, recording := range parsed.Recordings {
		var credit strings.Builder
		for _, name := range recording.ArtistCredit {
			credit.WriteString(name.Name + name.JoinPhrase)
		}
		match := RecordingMatch{
			RecordingMBID: recording.ID, Title: recording.Title, ArtistCredit: credit.String(),
			DurationMs: firstNonNil(recording.Length),
		}
		seen := make(map[string]bool)
		for _, release := range recording.Releases {
			rg := release.ReleaseGroup
			if rg.ID == "" || seen[rg.ID] {
				continue
			}
			seen[rg.ID] = true
			match.ReleaseGroups = append(match.ReleaseGroups, ReleaseGroup{
				MBID: rg.ID, Title: rg.Title, PrimaryType: rg.PrimaryType, SecondaryTypes: rg.SecondaryTypes,
			})
		}
		matches[i] = match
	}
	return matches, nil
}

// directBrowseResponse is musicbrainz.org's release-group browse JSON
// (`/release-group?artist=...`), which differs from the search endpoint's
// shape by name only -- both list "release-groups", but browsing also
// reports the total count so paging can stop without an extra request.
type directBrowseResponse struct {
	ReleaseGroupCount int                        `json:"release-group-count"`
	ReleaseGroups     []directBrowseReleaseGroup `json:"release-groups"`
}

type directBrowseReleaseGroup struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	PrimaryType      string   `json:"primary-type"`
	SecondaryTypes   []string `json:"secondary-types"`
	FirstReleaseDate string   `json:"first-release-date"`
}

// artistReleaseGroupsPageSize is musicbrainz.org's own per-request maximum.
const artistReleaseGroupsPageSize = 100

// artistReleaseGroupsMaxPages bounds a pathological discography (a
// various-artists series, or a prolific bootleg-heavy artist, can run into
// the thousands) rather than looping against a real API without a ceiling.
const artistReleaseGroupsMaxPages = 20

// ArtistReleaseGroups implements Discography by browsing musicbrainz.org's
// release-group endpoint, paging until either a page comes back short or the
// reported total is reached.
func (d direct) ArtistReleaseGroups(ctx context.Context, artistMBID string) ([]ReleaseGroup, error) {
	var all []ReleaseGroup
	for page := 0; page < artistReleaseGroupsMaxPages; page++ {
		offset := page * artistReleaseGroupsPageSize
		requestURL := fmt.Sprintf("%s/release-group?artist=%s&limit=%d&offset=%d&fmt=json",
			d.baseURL, url.QueryEscape(artistMBID), artistReleaseGroupsPageSize, offset)

		var parsed directBrowseResponse
		if err := getJSON(ctx, d.client, requestURL, &parsed); err != nil {
			return nil, fmt.Errorf("musicbrainz: artist release groups %s: %w", artistMBID, err)
		}
		for _, rg := range parsed.ReleaseGroups {
			all = append(all, ReleaseGroup{
				MBID: rg.ID, Title: rg.Title, PrimaryType: rg.PrimaryType,
				SecondaryTypes: rg.SecondaryTypes, FirstReleaseDate: rg.FirstReleaseDate,
			})
		}
		if len(parsed.ReleaseGroups) < artistReleaseGroupsPageSize ||
			offset+len(parsed.ReleaseGroups) >= parsed.ReleaseGroupCount {
			break
		}
	}
	return all, nil
}

// luceneQuoted wraps a value in double quotes for a MusicBrainz Lucene query,
// escaping the two characters that would otherwise break out of the quoted
// phrase or be read as Lucene operators.
func luceneQuoted(value string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value)
	return `"` + escaped + `"`
}

// directRelease is musicbrainz.org's release JSON with inc=recordings+labels+media.
// Field names are hyphenated -- MusicBrainz's own convention, not a typo.
type directRelease struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Date      string `json:"date"`
	Status    string `json:"status"`
	Country   string `json:"country"`
	Barcode   string `json:"barcode"`
	LabelInfo []struct {
		Label struct {
			Name string `json:"name"`
		} `json:"label"`
		CatalogNumber string `json:"catalog-number"`
	} `json:"label-info"`
	Media []struct {
		Position int    `json:"position"`
		Format   string `json:"format"`
		Tracks   []struct {
			Number    string `json:"number"`
			Position  int    `json:"position"`
			Title     string `json:"title"`
			Length    *int64 `json:"length"`
			Recording struct {
				ID     string `json:"id"`
				Title  string `json:"title"`
				Length *int64 `json:"length"`
			} `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
}

func (d direct) release(ctx context.Context, mbid string) (Release, error) {
	var parsed directRelease
	url := fmt.Sprintf("%s/release/%s?fmt=json&inc=recordings+labels+media", d.baseURL, mbid)
	if err := getJSON(ctx, d.client, url, &parsed); err != nil {
		return Release{}, fmt.Errorf("musicbrainz: release %s: %w", mbid, err)
	}
	return parsed.toRelease(), nil
}

// directReleaseBrowseLimit is MusicBrainz's own maximum page size for a browse.
const directReleaseBrowseLimit = 100

// Releases implements ReleaseLister with one browse request: every release of
// the group, tracklists included. A group with more than one page of releases
// is judged on its first page.
func (d direct) Releases(ctx context.Context, releaseGroupMBID string) ([]Release, error) {
	var parsed struct {
		Releases []directRelease `json:"releases"`
	}
	url := fmt.Sprintf("%s/release?release-group=%s&fmt=json&inc=recordings+labels+media&limit=%d",
		d.baseURL, releaseGroupMBID, directReleaseBrowseLimit)
	if err := getJSON(ctx, d.client, url, &parsed); err != nil {
		return nil, fmt.Errorf("musicbrainz: releases of %s: %w", releaseGroupMBID, err)
	}
	releases := make([]Release, len(parsed.Releases))
	for index, one := range parsed.Releases {
		releases[index] = one.toRelease()
	}
	return releases, nil
}

func (parsed directRelease) toRelease() Release {
	release := Release{
		MBID: parsed.ID, Title: parsed.Title, Status: strings.ToLower(parsed.Status),
		Date: parsed.Date, Country: parsed.Country, Barcode: parsed.Barcode,
	}
	if len(parsed.LabelInfo) > 0 {
		release.LabelName = parsed.LabelInfo[0].Label.Name
		release.CatalogueNumber = parsed.LabelInfo[0].CatalogNumber
	}
	for _, medium := range parsed.Media {
		out := Medium{Position: medium.Position, Format: medium.Format}
		for _, track := range medium.Tracks {
			title := track.Title
			if title == "" {
				title = track.Recording.Title
			}
			out.Tracks = append(out.Tracks, Track{
				Position: track.Position, Number: track.Number, Title: title,
				DurationMs:    firstNonNil(track.Length, track.Recording.Length),
				RecordingMBID: track.Recording.ID,
			})
		}
		release.Media = append(release.Media, out)
	}
	return release
}

// getJSON is the common request shape both sources use: GET, bounded read,
// decode. Shared rather than duplicated per source.
func getJSON(ctx context.Context, client *httpx.Client, url string, into any) error {
	response, err := client.Get(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// maxResponseBytes bounds what one lookup can make this process read. The largest
// real release-with-recordings response is well under this; a provider answering
// with more is not a release this package can use.
const maxResponseBytes = 4 << 20

func firstNonNil(values ...*int64) int64 {
	for _, v := range values {
		if v != nil {
			return *v
		}
	}
	return 0
}
