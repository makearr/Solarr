package wikidata

import (
	"context"
	"regexp"
	"strings"
)

// labelsPerQuery bounds the MusicBrainz label ids one query names: 200 made a URL the query service refused (414).
const labelsPerQuery = 50

// labelLogoWidth asks Commons for a raster at this width, so an SVG logo comes back as a picture the decoder reads.
const labelLogoWidth = "500"

var musicBrainzIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// LabelLogos answers the logo (P154) of the item each MusicBrainz label id (P966) names, for the labels that have one.
func (f Fetcher) LabelLogos(ctx context.Context, mbids []string) (map[string]string, error) {
	logos := map[string]string{}
	var valid []string
	for _, mbid := range mbids {
		if musicBrainzIDPattern.MatchString(mbid) {
			valid = append(valid, `"`+mbid+`"`)
		}
	}
	for start := 0; start < len(valid); start += labelsPerQuery {
		chunk := valid[start:min(start+labelsPerQuery, len(valid))]
		rows, err := f.sparql(ctx, "SELECT ?mbid ?logo WHERE { VALUES ?mbid { "+strings.Join(chunk, " ")+" } ?item wdt:P966 ?mbid; wdt:P154 ?logo. }")
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			file, found := strings.CutPrefix(row["logo"].Value, "http://commons.wikimedia.org/wiki/Special:FilePath/")
			if mbid := row["mbid"].Value; found && logos[mbid] == "" {
				logos[mbid] = commonsFilePathBase + file + "?width=" + labelLogoWidth
			}
		}
	}
	return logos, nil
}
