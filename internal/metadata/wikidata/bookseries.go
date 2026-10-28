package wikidata

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var openLibraryWorkPattern = regexp.MustCompile(`^OL[0-9]{1,12}W$`)

// SeriesBook is one book of a series Wikidata knows (P179), with the Open Library work ids (P648) it carries.
type SeriesBook struct {
	SeriesQID, SeriesName string
	QID, Title            string
	// OpenLibrary are its work ids, several where Open Library split a book; an edition's id is left out.
	OpenLibrary []string
	// Ordinal is the book's place in the series (P1545) as Wikidata writes it, "" when none is given.
	Ordinal string
	Year    int
}

// BookSeries answers every book of each series one of the Open Library works is part of, one per book and series.
func (f Fetcher) BookSeries(ctx context.Context, openLibraryIDs []string) ([]SeriesBook, error) {
	var valid []string
	for _, id := range openLibraryIDs {
		if openLibraryWorkPattern.MatchString(id) {
			valid = append(valid, `"`+id+`"`)
		}
	}
	if len(valid) == 0 {
		return nil, nil
	}
	rows, err := f.sparql(ctx, "SELECT DISTINCT ?series ?seriesLabel ?member ?memberLabel ?ol ?ordinal ?date WHERE { VALUES ?held { "+
		strings.Join(valid, " ")+" } ?book wdt:P648 ?held; wdt:P179 ?series. ?member p:P179 ?statement. ?statement ps:P179 ?series."+
		" OPTIONAL { ?statement pq:P1545 ?ordinal. } OPTIONAL { ?member wdt:P648 ?ol. } OPTIONAL { ?member wdt:P577 ?date. }"+
		` SERVICE wikibase:label { bd:serviceParam wikibase:language "en". } }`)
	if err != nil {
		return nil, err
	}
	at := map[string]int{}
	var books []SeriesBook
	for _, row := range rows {
		series, member, title := qidOf(row["series"].Value), qidOf(row["member"].Value), row["memberLabel"].Value
		if title == "" || title == member {
			continue
		}
		key := series + "/" + member
		index, seen := at[key]
		if !seen {
			index = len(books)
			at[key] = index
			books = append(books, SeriesBook{SeriesQID: series, SeriesName: row["seriesLabel"].Value, QID: member, Title: title, Ordinal: row["ordinal"].Value})
		}
		book := &books[index]
		if id := row["ol"].Value; openLibraryWorkPattern.MatchString(id) && !slices.Contains(book.OpenLibrary, id) {
			book.OpenLibrary = append(book.OpenLibrary, id)
		}
		if date := row["date"].Value; len(date) >= 4 {
			if year, err := strconv.Atoi(date[:4]); err == nil && (book.Year == 0 || year < book.Year) {
				book.Year = year
			}
		}
	}
	return books, nil
}
