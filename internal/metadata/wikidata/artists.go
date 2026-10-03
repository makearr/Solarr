package wikidata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// musicBrainzArtistProperty is P434, Wikidata's own "MusicBrainz artist ID" claim.
const musicBrainzArtistProperty = "P434"

// MusicBrainzArtists answers the MusicBrainz artist id each QID carries, for the ones that carry one: the link from a
// person known elsewhere (an actor on TMDB) to the same human as a music artist. One request per fifty QIDs.
func (f Fetcher) MusicBrainzArtists(ctx context.Context, qids []string) (map[string]string, error) {
	answers := make(map[string]string, len(qids))
	for start := 0; start < len(qids); start += maxEntitiesPerRequest {
		chunk := qids[start:min(start+maxEntitiesPerRequest, len(qids))]
		query := url.Values{"action": {"wbgetentities"}, "ids": {strings.Join(chunk, "|")}, "props": {"claims"}, "format": {"json"}}
		var entities entitiesResponse
		if err := getJSON(ctx, f.wikidataClient, "https://www.wikidata.org/w/api.php?"+query.Encode(), &entities); err != nil {
			return nil, fmt.Errorf("wikidata: artist ids %s: %w", strings.Join(chunk, ","), err)
		}
		for _, qid := range chunk {
			if id := firstStringClaim(entities.Entities[qid].Claims, musicBrainzArtistProperty); id != "" {
				answers[qid] = id
			}
		}
	}
	return answers, nil
}

// firstStringClaim is the first value of a string-valued property.
func firstStringClaim(claims map[string]json.RawMessage, property string) string {
	raw, found := claims[property]
	if !found {
		return ""
	}
	var values []struct {
		Mainsnak struct {
			Datavalue struct {
				Value string `json:"value"`
			} `json:"datavalue"`
		} `json:"mainsnak"`
	}
	if err := json.Unmarshal(raw, &values); err != nil || len(values) == 0 {
		return ""
	}
	return values[0].Mainsnak.Datavalue.Value
}
