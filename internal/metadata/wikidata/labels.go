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

// namesPerQuery bounds the names one query lists; a name search walks every organisation class, so batches stay small.
const namesPerQuery = 25

// OrganisationLogos answers the logo (P154) of the organisation (Q43229 or a subclass) whose English label is each name, for
// the names that have one; a name that matches several organisations takes the first the service lists.
func (f Fetcher) OrganisationLogos(ctx context.Context, names []string) (map[string]string, error) {
	logos := map[string]string{}
	var quoted []string
	for _, name := range names {
		if literal, ok := sparqlLiteral(name); ok {
			quoted = append(quoted, literal+"@en")
		}
	}
	for start := 0; start < len(quoted); start += namesPerQuery {
		chunk := quoted[start:min(start+namesPerQuery, len(quoted))]
		rows, err := f.sparql(ctx, "SELECT ?name ?logo WHERE { VALUES ?name { "+strings.Join(chunk, " ")+" } ?item rdfs:label ?name; wdt:P154 ?logo; wdt:P31/wdt:P279* wd:Q43229. }")
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			file, found := strings.CutPrefix(row["logo"].Value, "http://commons.wikimedia.org/wiki/Special:FilePath/")
			if name := row["name"].Value; found && logos[name] == "" {
				logos[name] = commonsFilePathBase + file + "?width=" + labelLogoWidth
			}
		}
	}
	return logos, nil
}

// sparqlLiteral is name as a quoted SPARQL string; a name with a control character is refused rather than escaped.
func sparqlLiteral(name string) (string, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 120 {
		return "", false
	}
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name) + `"`, true
}
