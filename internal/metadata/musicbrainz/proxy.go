package musicbrainz

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// ProxyName is the ADR-13 provider bucket for the Servarr metadata proxy --
// ADR-29's preferred, never-required first source.
const ProxyName = "musicbrainz-proxy"

// proxy talks to api.lidarr.audio/api/v0.4. One request returns a release group's
// full detail plus every release under it, tracks included -- cheaper than the
// direct source's two calls, which is exactly why ADR-29 tries it first.
type proxy struct {
	client  *httpx.Client
	baseURL string
}

// NewProxy builds the proxy source. client's Provider must be ProxyName.
func NewProxy(client *httpx.Client, baseURL string) Source {
	return proxy{client: client, baseURL: baseURL}
}

func (proxy) Name() string { return ProxyName }

// proxyAlbum is api.lidarr.audio's response shape for /album/{mbid}. Casing is
// exactly as the service returns it -- lowercase at the top level, PascalCase
// inside Releases -- confirmed against a live response, not a typo here.
type proxyAlbum struct {
	ID             string             `json:"id"`
	Title          string             `json:"title"`
	Type           string             `json:"type"`
	SecondaryTypes []string           `json:"secondarytypes"`
	ReleaseDate    string             `json:"releasedate"`
	ArtistID       string             `json:"artistid"`
	Artists        []proxyAlbumArtist `json:"artists"`
	Genres         []string           `json:"genres"`
	Links          []proxyLink        `json:"links"`
	Releases       []proxyRelease     `json:"Releases"`
}

type proxyAlbumArtist struct {
	ID   string `json:"id"`
	Name string `json:"artistname"`
}

type proxyLink struct {
	Target string `json:"target"`
	Type   string `json:"type"`
}

type proxyRelease struct {
	ID          string       `json:"Id"`
	Title       string       `json:"Title"`
	Status      string       `json:"Status"`
	Country     []string     `json:"Country"`
	Label       []string     `json:"Label"`
	Media       []proxyMedia `json:"Media"`
	ReleaseDate string       `json:"ReleaseDate"`
	Tracks      []proxyTrack `json:"Tracks"`
}

type proxyMedia struct {
	Position int    `json:"Position"`
	Format   string `json:"Format"`
}

type proxyTrack struct {
	RecordingID   string `json:"RecordingId"`
	TrackName     string `json:"TrackName"`
	TrackNumber   string `json:"TrackNumber"`
	TrackPosition int    `json:"TrackPosition"`
	DurationMs    int64  `json:"DurationMs"`
	MediumNumber  int    `json:"MediumNumber"`
}

// An empty releaseMBID answers the release group alone (a zero Release, no
// error) -- section 21.3's manual-resolve action only ever has a release
// group id to confirm from the unidentified queue, never a specific pressing.
func (p proxy) Album(ctx context.Context, releaseGroupMBID, releaseMBID string) (ReleaseGroup, Release, error) {
	var parsed proxyAlbum
	url := fmt.Sprintf("%s/album/%s", p.baseURL, releaseGroupMBID)
	if err := getJSON(ctx, p.client, url, &parsed); err != nil {
		return ReleaseGroup{}, Release{}, fmt.Errorf("musicbrainz-proxy: album %s: %w", releaseGroupMBID, err)
	}

	rg := ReleaseGroup{
		MBID: parsed.ID, Title: parsed.Title,
		PrimaryType: parsed.Type, SecondaryTypes: parsed.SecondaryTypes, FirstReleaseDate: parsed.ReleaseDate,
		ArtistCredit: proxyArtistCredit(parsed),
		Genres:       proxyGenres(parsed.Genres),
		WikidataQID:  proxyWikidataQID(parsed.Links),
	}
	if releaseMBID == "" {
		return rg, Release{}, nil
	}

	for _, candidate := range parsed.Releases {
		if candidate.ID == releaseMBID {
			return rg, toRelease(candidate), nil
		}
	}
	return ReleaseGroup{}, Release{}, fmt.Errorf(
		"musicbrainz-proxy: release %s is not under release group %s", releaseMBID, releaseGroupMBID)
}

// proxyArtistCredit is the album's artists, its own primary artist (artistid)
// first, since credit order decides a work's primary artist. These were never
// read, so an album enriched through the proxy -- the default first provider --
// got no artist, and a fresh library had no artists at all (found 2026-09-17).
func proxyArtistCredit(album proxyAlbum) []ArtistCredit {
	credits := make([]ArtistCredit, 0, len(album.Artists))
	for _, artist := range album.Artists {
		if artist.ID == album.ArtistID {
			credits = append(credits, ArtistCredit{MBID: artist.ID, Name: artist.Name, ArtistName: artist.Name})
		}
	}
	for _, artist := range album.Artists {
		if artist.ID != album.ArtistID {
			credits = append(credits, ArtistCredit{MBID: artist.ID, Name: artist.Name, ArtistName: artist.Name})
		}
	}
	return credits
}

// proxyGenres keeps the proxy's order, best first; it reports no vote counts.
func proxyGenres(names []string) []Genre {
	genres := make([]Genre, len(names))
	for index, name := range names {
		genres[index] = Genre{Name: name}
	}
	return genres
}

func proxyWikidataQID(links []proxyLink) string {
	for _, link := range links {
		if link.Type == "wikidata" {
			return wikidataQID(link.Target)
		}
	}
	return ""
}

// Releases implements ReleaseLister from the same album response Album reads,
// which carries every release with its tracks.
func (p proxy) Releases(ctx context.Context, releaseGroupMBID string) ([]Release, error) {
	var parsed proxyAlbum
	url := fmt.Sprintf("%s/album/%s", p.baseURL, releaseGroupMBID)
	if err := getJSON(ctx, p.client, url, &parsed); err != nil {
		return nil, fmt.Errorf("musicbrainz-proxy: album %s: %w", releaseGroupMBID, err)
	}
	releases := make([]Release, len(parsed.Releases))
	for index, candidate := range parsed.Releases {
		releases[index] = toRelease(candidate)
	}
	return releases, nil
}

func toRelease(r proxyRelease) Release {
	release := Release{
		MBID: r.ID, Title: r.Title, Status: strings.ToLower(r.Status), Date: r.ReleaseDate,
	}
	if len(r.Country) > 0 {
		release.Country = r.Country[0]
	}
	if len(r.Label) > 0 {
		release.LabelName = r.Label[0]
	}

	byMedium := make(map[int][]Track, len(r.Media))
	for _, track := range r.Tracks {
		byMedium[track.MediumNumber] = append(byMedium[track.MediumNumber], Track{
			Position: track.TrackPosition, Number: track.TrackNumber, Title: track.TrackName,
			DurationMs: track.DurationMs, RecordingMBID: track.RecordingID,
		})
	}

	positions := make([]int, 0, len(byMedium))
	for position := range byMedium {
		positions = append(positions, position)
	}
	sort.Ints(positions)

	formats := make(map[int]string, len(r.Media))
	for _, medium := range r.Media {
		formats[medium.Position] = medium.Format
	}

	for _, position := range positions {
		tracks := byMedium[position]
		sort.Slice(tracks, func(i, j int) bool { return tracks[i].Position < tracks[j].Position })
		release.Media = append(release.Media, Medium{
			Position: position, Format: formats[position], Tracks: tracks,
		})
	}
	return release
}
