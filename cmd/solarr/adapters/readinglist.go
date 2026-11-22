package adapters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/identify/readinglist"
)

// ReadingLists matches a reading list to a comic library's held series and issues.
type ReadingLists struct {
	Reader library.SeriesReader
	Series library.Series
}

// Preview implements apiserver.ReadingLists.
func (a ReadingLists) Preview(ctx context.Context, categoryID int64, content string) (gen.ReadingListPreview, error) {
	list, err := readinglist.ParseReadingList(strings.NewReader(content))
	if errors.Is(err, readinglist.ErrReadingList) {
		return gen.ReadingListPreview{}, fmt.Errorf("%w: %w", apiserver.ErrBadReadingList, err)
	}
	if err != nil {
		return gen.ReadingListPreview{}, err
	}
	held, err := a.Reader.TitlesOf(ctx, "comic", categoryID)
	if err != nil {
		return gen.ReadingListPreview{}, err
	}
	titles := make([]readinglist.SeriesTitle, len(held))
	for i, title := range held {
		titles[i] = readinglist.SeriesTitle{WorkID: title.WorkID, Title: title.Title, Year: title.Year, Canonical: title.Canonical}
	}
	var failed error
	matches := readinglist.MatchReadingList(list, titles, func(workID int64, ref string) (int64, bool) {
		parts, absent, err := a.Series.ResolveParts(ctx, workID, []library.PartKey{{MediaRef: ref}})
		if err != nil {
			failed = err
			return 0, false
		}
		if absent > 0 || len(parts) == 0 {
			return 0, false
		}
		return parts[0].PartID, true
	})
	if failed != nil {
		return gen.ReadingListPreview{}, failed
	}
	books := make([]previewBook, len(matches))
	for i, match := range matches {
		book := previewBook{Series: match.Book.Series, Number: match.Book.Number, Status: match.Status}
		if match.Book.Volume != "" {
			book.Volume = &match.Book.Volume
		}
		if match.Missing != "" {
			book.Missing = &match.Missing
		}
		if match.WorkID != 0 {
			book.WorkID, book.SeriesTitle = &match.WorkID, &match.Series
		}
		if match.PartID != 0 {
			book.PartID = &match.PartID
		}
		books[i] = book
	}
	encoded, err := json.Marshal(struct {
		Name  string        `json:"name"`
		Books []previewBook `json:"books"`
	}{list.Name, books})
	if err != nil {
		return gen.ReadingListPreview{}, err
	}
	var preview gen.ReadingListPreview
	return preview, json.Unmarshal(encoded, &preview)
}

// previewBook is one book of the preview in the shape of the API's JSON.
type previewBook struct {
	Series      string  `json:"series"`
	Number      string  `json:"number"`
	Volume      *string `json:"volume,omitempty"`
	Status      string  `json:"status"`
	Missing     *string `json:"missing,omitempty"`
	WorkID      *int64  `json:"work_id,omitempty"`
	SeriesTitle *string `json:"series_title,omitempty"`
	PartID      *int64  `json:"part_id,omitempty"`
}
