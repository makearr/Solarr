package indexer

import (
	"context"
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
)

// capsXML is one t=caps response -- the Newznab specification's own shape,
// shared by Torznab (ADR-43). Only the fields this package actually reads
// are declared; an indexer's own additional <server>/<limits>/<registration>
// blocks are ignored rather than modelled.
type capsXML struct {
	XMLName    xml.Name          `xml:"caps"`
	Searching  capsSearchingXML  `xml:"searching"`
	Categories capsCategoriesXML `xml:"categories"`
}

type capsSearchingXML struct {
	Search      capsFunctionXML `xml:"search"`
	TVSearch    capsFunctionXML `xml:"tv-search"`
	MovieSearch capsFunctionXML `xml:"movie-search"`
	MusicSearch capsFunctionXML `xml:"music-search"`
	BookSearch  capsFunctionXML `xml:"book-search"`
	AudioSearch capsFunctionXML `xml:"audio-search"`
}

type capsFunctionXML struct {
	Available       string `xml:"available,attr"`
	SupportedParams string `xml:"supportedParams,attr"`
}

func (f capsFunctionXML) toSearchFunction() SearchFunction {
	var params []string
	if f.SupportedParams != "" {
		params = strings.Split(f.SupportedParams, ",")
	}
	return SearchFunction{Available: f.Available == "yes", SupportedParams: params}
}

type capsCategoriesXML struct {
	Category []capsCategoryXML `xml:"category"`
}

type capsCategoryXML struct {
	ID     string            `xml:"id,attr"`
	Name   string            `xml:"name,attr"`
	Subcat []capsCategoryXML `xml:"subcat"`
}

func (c capsCategoryXML) toCategory() Category {
	id, _ := strconv.Atoi(c.ID)
	category := Category{ID: id, Name: c.Name}
	for _, sub := range c.Subcat {
		category.Subcategories = append(category.Subcategories, sub.toCategory())
	}
	return category
}

// Capabilities fetches and parses t=caps -- ADR-43's own "categories are
// taken from the response, never assumed", so a caller (indexer connection
// setup, or a periodic refresh) always has the indexer's own real answer
// rather than a guess.
func (c Client) Capabilities(ctx context.Context) (Capabilities, error) {
	requestURL := c.apiURL("caps", nil)
	body, err := c.getBody(ctx, requestURL)
	if err != nil {
		return Capabilities{}, fmt.Errorf("indexer: fetch capabilities: %w", err)
	}

	var parsed capsXML
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return Capabilities{}, fmt.Errorf("indexer: parse capabilities: %w", err)
	}

	categories := make([]Category, len(parsed.Categories.Category))
	for i, category := range parsed.Categories.Category {
		categories[i] = category.toCategory()
	}

	return Capabilities{
		Categories: categories,
		Search: map[string]SearchFunction{
			"search":       parsed.Searching.Search.toSearchFunction(),
			"tv-search":    parsed.Searching.TVSearch.toSearchFunction(),
			"movie-search": parsed.Searching.MovieSearch.toSearchFunction(),
			"music-search": parsed.Searching.MusicSearch.toSearchFunction(),
			"book-search":  parsed.Searching.BookSearch.toSearchFunction(),
			"audio-search": parsed.Searching.AudioSearch.toSearchFunction(),
		},
	}, nil
}
