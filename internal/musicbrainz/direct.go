package musicbrainz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/makearr/MediaNexus/internal/httpx"
)

// DirectName is the ADR-13 provider bucket for musicbrainz.org itself -- the
// fallback ADR-29 says the proxy must be able to fail over to, never optional.
const DirectName = "musicbrainz"

// direct talks to musicbrainz.org/ws/2 (ADR-29): one request per second, meant
// literally, enforced by the shared httpx.Limiter the caller configures the
// client with, not by anything in this package.
type direct struct {
	client  *httpx.Client
	baseURL string
}

// NewDirect builds the direct source. client's Provider must be DirectName.
func NewDirect(client *httpx.Client, baseURL string) Source {
	return direct{client: client, baseURL: baseURL}
}

func (direct) Name() string { return DirectName }

func (d direct) Album(ctx context.Context, releaseGroupMBID, releaseMBID string) (ReleaseGroup, Release, error) {
	rg, err := d.releaseGroup(ctx, releaseGroupMBID)
	if err != nil {
		return ReleaseGroup{}, Release{}, err
	}
	release, err := d.release(ctx, releaseMBID)
	if err != nil {
		return ReleaseGroup{}, Release{}, err
	}
	return rg, release, nil
}

// directReleaseGroup is musicbrainz.org's release-group JSON. Its own default
// fields already carry everything this package wants -- no inc= needed.
type directReleaseGroup struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	PrimaryType      string `json:"primary-type"`
	FirstReleaseDate string `json:"first-release-date"`
}

func (d direct) releaseGroup(ctx context.Context, mbid string) (ReleaseGroup, error) {
	var parsed directReleaseGroup
	url := fmt.Sprintf("%s/release-group/%s?fmt=json", d.baseURL, mbid)
	if err := getJSON(ctx, d.client, url, &parsed); err != nil {
		return ReleaseGroup{}, fmt.Errorf("musicbrainz: release-group %s: %w", mbid, err)
	}
	return ReleaseGroup{
		MBID: parsed.ID, Title: parsed.Title,
		PrimaryType: parsed.PrimaryType, FirstReleaseDate: parsed.FirstReleaseDate,
	}, nil
}

// directSearchResponse is musicbrainz.org's release-group search JSON
// (`/release-group/?query=...`) -- verified against a real live response.
type directSearchResponse struct {
	ReleaseGroups []directSearchReleaseGroup `json:"release-groups"`
}

type directSearchReleaseGroup struct {
	ID               string `json:"id"`
	Title            string `json:"title"`
	PrimaryType      string `json:"primary-type"`
	FirstReleaseDate string `json:"first-release-date"`
	ArtistCredit     []struct {
		Name string `json:"name"`
	} `json:"artist-credit"`
}

// SearchReleaseGroups implements Searcher against musicbrainz.org's Lucene search
// endpoint, scoped to artist and release-group (album) title -- the two fields a
// caller with no MBID at all can actually claim to know (section 10.2).
func (d direct) SearchReleaseGroups(ctx context.Context, artist, title string, limit int) ([]ReleaseGroupMatch, error) {
	query := fmt.Sprintf(`artist:%s AND releasegroup:%s`, luceneQuoted(artist), luceneQuoted(title))
	requestURL := fmt.Sprintf("%s/release-group/?query=%s&fmt=json&limit=%d",
		d.baseURL, url.QueryEscape(query), limit)

	var parsed directSearchResponse
	if err := getJSON(ctx, d.client, requestURL, &parsed); err != nil {
		return nil, fmt.Errorf("musicbrainz: search release-group %q/%q: %w", artist, title, err)
	}

	matches := make([]ReleaseGroupMatch, len(parsed.ReleaseGroups))
	for i, rg := range parsed.ReleaseGroups {
		var creditNames []string
		for _, credit := range rg.ArtistCredit {
			creditNames = append(creditNames, credit.Name)
		}
		matches[i] = ReleaseGroupMatch{
			ReleaseGroup: ReleaseGroup{
				MBID: rg.ID, Title: rg.Title,
				PrimaryType: rg.PrimaryType, FirstReleaseDate: rg.FirstReleaseDate,
			},
			ArtistCredit: strings.Join(creditNames, " & "),
		}
	}
	return matches, nil
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
	return release, nil
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
