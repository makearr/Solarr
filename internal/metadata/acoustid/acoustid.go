// Package acoustid looks a Chromaprint fingerprint up against acoustid.org
// (ADR-29's fingerprint rung, D12). It identifies and nothing more: what comes
// back is a set of MusicBrainz ids, which the caller then resolves through the
// ordered metadata sources like any other candidate.
package acoustid

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/platform/httpx"
)

// Name is this source's own rate-limit bucket and breaker key (ADR-13).
const Name = "acoustid"

// DefaultBaseURL is acoustid.org's own v2 endpoint.
const DefaultBaseURL = "https://api.acoustid.org/v2"

// ErrNoKey reports that no API key is configured, so the rung is disabled
// rather than failing. D12: the rung is built now and the key is entered
// later; until then it reports itself disabled and files it skipped are
// re-run once one appears.
var ErrNoKey = errors.New("acoustid: no API key configured")

// MinScore is the floor a match must clear. AcoustID scores 0..1 and returns
// weak matches freely; below this they are guesses, and a wrong identity is
// worse than none (ADR-24 -- a network match is a guess with a confidence).
const MinScore = 0.70

// Match is one result: how well the fingerprint matched, and what MusicBrainz
// knows the audio to be.
type Match struct {
	// Score is AcoustID's own 0..1 confidence for the fingerprint itself.
	Score float64
	// Recordings are the recordings this audio matched. A recording is the
	// track, not the album, so the caller resolves upward from it -- which is
	// what the names are for: a file whose tags say nothing still has an artist
	// and a title once its audio has been recognised.
	Recordings []Recording
	// ReleaseGroupMBIDs are the albums those recordings appear on, when
	// AcoustID reported them.
	ReleaseGroupMBIDs []string
}

// Recording is one matched recording: its MusicBrainz id and the names that go
// with it. AcoustID identifies and nothing more (D12), so these are what a
// caller searches the ordered metadata sources with.
type Recording struct {
	MBID   string
	Title  string
	Artist string
	// Duration is what MusicBrainz holds for the recording, not the file's own.
	Duration time.Duration
	// ReleaseGroupMBIDs are the albums this recording appears on. Held per
	// recording, not merged: one fingerprint can name recordings by different
	// artists entirely, and their albums must not be pooled.
	ReleaseGroupMBIDs []string
}

// Client queries acoustid.org.
type Client struct {
	http    *httpx.Client
	baseURL string
	// key is read per call rather than captured, so entering one in the
	// interface takes effect without a restart (D12).
	key func() string
}

// New builds a client. baseURL empty means DefaultBaseURL.
func New(client *httpx.Client, baseURL string, key func() string) Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return Client{http: client, baseURL: strings.TrimSuffix(baseURL, "/"), key: key}
}

// Enabled reports whether a key is configured. Callers use it to say "disabled:
// no key" rather than reporting a lookup failure for something never set up.
func (c Client) Enabled() bool { return c.key != nil && c.key() != "" }

// Lookup answers matches for one fingerprint, best first, dropping anything
// below MinScore. A fingerprint with no match at all is not an error: most
// files simply are not in AcoustID.
func (c Client) Lookup(ctx context.Context, fingerprint string, duration time.Duration) ([]Match, error) {
	if !c.Enabled() {
		return nil, ErrNoKey
	}
	if fingerprint == "" || duration <= 0 {
		return nil, fmt.Errorf("acoustid: lookup needs a fingerprint and a duration")
	}

	query := url.Values{}
	query.Set("client", c.key())
	query.Set("fingerprint", fingerprint)
	query.Set("duration", strconv.Itoa(int(duration.Seconds())))
	// recordings gives the ids; releasegroups the albums they sit on. Both are
	// wanted, because a recording alone does not say which album it belongs to.
	// Space-separated, never "+": a literal plus is not the separator, and
	// AcoustID then answers bare ids with no metadata at all rather than failing.
	query.Set("meta", "recordings releasegroups")
	query.Set("format", "json")

	var decoded response
	if err := c.getJSON(ctx, c.baseURL+"/lookup?"+query.Encode(), &decoded); err != nil {
		return nil, fmt.Errorf("acoustid: lookup: %w", err)
	}
	if decoded.Status != "ok" {
		return nil, fmt.Errorf("acoustid: lookup refused: %s", decoded.Error.Message)
	}

	matches := make([]Match, 0, len(decoded.Results))
	for _, result := range decoded.Results {
		if result.Score < MinScore {
			continue
		}
		match := Match{Score: result.Score}
		seenGroup := map[string]bool{}
		for _, recording := range result.Recordings {
			var groups []string
			for _, group := range recording.ReleaseGroups {
				if group.ID == "" {
					continue
				}
				groups = append(groups, group.ID)
				if !seenGroup[group.ID] {
					seenGroup[group.ID] = true
					match.ReleaseGroupMBIDs = append(match.ReleaseGroupMBIDs, group.ID)
				}
			}
			if recording.ID != "" {
				match.Recordings = append(match.Recordings, Recording{
					MBID: recording.ID, Title: recording.Title, Artist: creditOf(recording.Artists),
					Duration:          time.Duration(recording.Duration * float64(time.Second)),
					ReleaseGroupMBIDs: groups,
				})
			}
		}
		if len(match.Recordings) == 0 && len(match.ReleaseGroupMBIDs) == 0 {
			// A scored match naming nothing identifies nothing.
			continue
		}
		matches = append(matches, match)
	}
	return matches, nil
}

// response is only the fields used; AcoustID returns considerably more.
type response struct {
	Status string `json:"status"`
	Error  struct {
		Message string `json:"message"`
	} `json:"error"`
	Results []struct {
		Score      float64 `json:"score"`
		Recordings []struct {
			ID            string           `json:"id"`
			Title         string           `json:"title"`
			Duration      float64          `json:"duration"`
			Artists       []responseArtist `json:"artists"`
			ReleaseGroups []struct {
				ID string `json:"id"`
			} `json:"releasegroups"`
		} `json:"recordings"`
	} `json:"results"`
}

// responseArtist is one name in a recording's artist credit.
type responseArtist struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
}

// creditOf joins an artist credit the way MusicBrainz itself renders one, so a
// collaboration reads "A & B" rather than losing everyone but the first.
func creditOf(artists []responseArtist) string {
	var credit strings.Builder
	for _, artist := range artists {
		credit.WriteString(artist.Name)
		credit.WriteString(artist.JoinPhrase)
	}
	return strings.TrimSpace(credit.String())
}

// maxResponseBytes bounds a reply nobody legitimately sends more than.
const maxResponseBytes = 4 << 20

func (c Client) getJSON(ctx context.Context, url string, into any) error {
	response, err := c.http.Get(ctx, url)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	return json.Unmarshal(body, into)
}
