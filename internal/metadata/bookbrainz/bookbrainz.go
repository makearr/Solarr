// Package bookbrainz reads BookBrainz (bookbrainz.org), the open MusicBrainz-style book database, keyless: works found
// by title with their authors, language and the annotation a contributor wrote. Coverage is thin; it fills what others lack.
package bookbrainz

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

const (
	baseURL          = "https://bookbrainz.org"
	maxResponseBytes = 8 << 20
	// SearchPageSize is the most works one search is asked for.
	SearchPageSize = 10
	// authorRelationship is the relationship type "author wrote work"; translators and editors have their own.
	authorRelationship = 8
)

// Getter is the one method of httpx.Client this package needs.
type Getter interface {
	GetJSON(ctx context.Context, address string, limit int64, into any) error
}

// Client asks BookBrainz.
type Client struct {
	http Getter
	base string
}

// New builds a client over an httpx client carrying BookBrainz's own rate bucket.
func New(client Getter) Client { return Client{http: client, base: baseURL} }

// Work is one literary work as BookBrainz holds it.
type Work struct {
	BBID string
	Name string
	// Disambiguation tells apart works of one name ("French translation").
	Disambiguation string
	// Type is the work's kind: Novel, Short Story, Poem.
	Type string
	// Language is an ISO 639-1 code, empty when the work names none.
	Language string
	// Annotation is the free text a contributor wrote about the work.
	Annotation string
	Authors    []string
}

// URL is the work's page.
func (w Work) URL() string { return baseURL + "/work/" + w.BBID }

type searchResponse struct {
	Results []struct {
		BBID           string `json:"bbid"`
		Name           string `json:"name"`
		WorkType       string `json:"workType"`
		Type           string `json:"type"`
		Disambiguation *struct {
			Comment string `json:"comment"`
		} `json:"disambiguation"`
		DefaultAlias *struct {
			Language *struct {
				ISOCode1 string `json:"isoCode1"`
			} `json:"language"`
		} `json:"defaultAlias"`
		Annotation *struct {
			Content string `json:"content"`
		} `json:"annotation"`
		RelationshipSet *struct {
			Relationships []struct {
				TypeID int `json:"typeId"`
				Source *struct {
					Name string `json:"name"`
					Type string `json:"type"`
				} `json:"source"`
			} `json:"relationships"`
		} `json:"relationshipSet"`
	} `json:"results"`
}

// Search answers the works whose name matches title, best match first.
func (c Client) Search(ctx context.Context, title string, limit int) ([]Work, error) {
	query := url.Values{"q": {title}, "type": {"work"}, "size": {strconv.Itoa(min(limit, SearchPageSize))}}
	var answer searchResponse
	if err := c.http.GetJSON(ctx, c.base+"/search/search?"+query.Encode(), maxResponseBytes, &answer); err != nil {
		return nil, err
	}
	works := make([]Work, 0, len(answer.Results))
	for _, hit := range answer.Results {
		if hit.Type != "Work" || hit.BBID == "" || strings.TrimSpace(hit.Name) == "" {
			continue
		}
		work := Work{BBID: hit.BBID, Name: strings.TrimSpace(hit.Name), Type: hit.WorkType}
		if hit.Disambiguation != nil {
			work.Disambiguation = hit.Disambiguation.Comment
		}
		if hit.DefaultAlias != nil && hit.DefaultAlias.Language != nil {
			work.Language = hit.DefaultAlias.Language.ISOCode1
		}
		if hit.Annotation != nil {
			work.Annotation = strings.TrimSpace(hit.Annotation.Content)
		}
		if hit.RelationshipSet != nil {
			for _, relationship := range hit.RelationshipSet.Relationships {
				if relationship.TypeID == authorRelationship && relationship.Source != nil && relationship.Source.Type == "Author" && relationship.Source.Name != "" {
					work.Authors = append(work.Authors, relationship.Source.Name)
				}
			}
		}
		works = append(works, work)
	}
	return works, nil
}
