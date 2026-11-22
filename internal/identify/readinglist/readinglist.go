// Package readinglist reads ComicRack reading lists (.cbl) and matches their books to a library's series and issues.
package readinglist

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SeriesTitle is one title (canonical or alias) a held series answers to.
type SeriesTitle struct {
	WorkID    int64
	Title     string
	Year      int64
	Canonical bool
}

// MaxReadingListBytes caps an uploaded reading list.
const MaxReadingListBytes = 1 << 20

// ReadingList is a ComicRack .cbl file: a name and its books in reading order.
type ReadingList struct {
	Name  string
	Books []ReadingBook
}

// ReadingBook is one entry; Volume is the year the series' volume started, as ComicRack writes it.
type ReadingBook struct {
	Series, Number, Volume, Year string
}

type cblFile struct {
	Name  string `xml:"Name"`
	Books struct {
		Book []struct {
			Series string `xml:"Series,attr"`
			Number string `xml:"Number,attr"`
			Volume string `xml:"Volume,attr"`
			Year   string `xml:"Year,attr"`
		} `xml:"Book"`
	} `xml:"Books"`
}

// ErrReadingList is a file that is not a reading list.
var ErrReadingList = errors.New("comic: not a reading list")

// ParseReadingList reads a .cbl file; encoding/xml resolves no external entities.
func ParseReadingList(r io.Reader) (ReadingList, error) {
	limited, err := io.ReadAll(io.LimitReader(r, MaxReadingListBytes+1))
	if err != nil {
		return ReadingList{}, fmt.Errorf("comic: reading list: %w", err)
	}
	if len(limited) > MaxReadingListBytes {
		return ReadingList{}, fmt.Errorf("%w: larger than %d bytes", ErrReadingList, MaxReadingListBytes)
	}
	var file cblFile
	if err := xml.Unmarshal(limited, &file); err != nil {
		return ReadingList{}, fmt.Errorf("%w: %w", ErrReadingList, err)
	}
	list := ReadingList{Name: strings.TrimSpace(file.Name)}
	for _, book := range file.Books.Book {
		if strings.TrimSpace(book.Series) == "" || strings.TrimSpace(book.Number) == "" {
			continue
		}
		list.Books = append(list.Books, ReadingBook{Series: strings.TrimSpace(book.Series), Number: strings.TrimSpace(book.Number),
			Volume: strings.TrimSpace(book.Volume), Year: strings.TrimSpace(book.Year)})
	}
	if len(list.Books) == 0 {
		return ReadingList{}, fmt.Errorf("%w: no books", ErrReadingList)
	}
	return list, nil
}

// Reading list match outcomes.
const (
	MatchFound     = "matched"
	MatchAmbiguous = "ambiguous"
	MatchNotFound  = "not_found"
)

// BookMatch is what one book of a reading list is in the library; PartID is set only when matched.
type BookMatch struct {
	Book    ReadingBook
	Status  string
	WorkID  int64
	PartID  int64
	Series  string
	Missing string
}

// MatchReadingList finds each book among the library's series (by title or alias, narrowed by the volume year) and
// its issue; part answers a series' part by media reference.
func MatchReadingList(list ReadingList, titles []SeriesTitle, part func(workID int64, ref string) (int64, bool)) []BookMatch {
	byKey := map[string][]SeriesTitle{}
	for _, title := range titles {
		key := titleKey(title.Title)
		byKey[key] = append(byKey[key], title)
	}
	canonical := map[int64]string{}
	for _, title := range titles {
		if title.Canonical {
			canonical[title.WorkID] = title.Title
		}
	}
	matches := make([]BookMatch, len(list.Books))
	for i, book := range list.Books {
		matches[i] = BookMatch{Book: book, Status: MatchNotFound, Missing: "series"}
		candidates := distinctWorks(byKey[titleKey(book.Series)])
		if len(candidates) > 1 && book.Volume != "" {
			var sameYear []SeriesTitle
			for _, candidate := range candidates {
				if fmt.Sprint(candidate.Year) == book.Volume {
					sameYear = append(sameYear, candidate)
				}
			}
			if len(sameYear) > 0 {
				candidates = sameYear
			}
		}
		switch {
		case len(candidates) == 0:
			continue
		case len(candidates) > 1:
			matches[i].Status, matches[i].Missing = MatchAmbiguous, ""
			continue
		}
		work := candidates[0].WorkID
		matches[i].WorkID, matches[i].Series = work, canonical[work]
		if id, found := part(work, "issue:"+book.Number); found {
			matches[i].Status, matches[i].PartID, matches[i].Missing = MatchFound, id, ""
		} else {
			matches[i].Missing = "issue"
		}
	}
	return matches
}

func distinctWorks(titles []SeriesTitle) []SeriesTitle {
	seen := map[int64]bool{}
	var out []SeriesTitle
	for _, title := range titles {
		if !seen[title.WorkID] {
			seen[title.WorkID] = true
			out = append(out, title)
		}
	}
	return out
}

// titleKey is a title as comparable text: lower case letters and digits only.
func titleKey(title string) string {
	var out strings.Builder
	for _, r := range strings.ToLower(title) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			out.WriteRune(r)
		}
	}
	return out.String()
}
