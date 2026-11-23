package comic

import (
	"context"
	"strconv"
	"strings"

	"github.com/makearr/Solarr/internal/metadata/specprovider"
)

// ConsumesRecords implements media.RecordConsuming.
func (Module) ConsumesRecords() bool { return true }

// RecordProvider is a definition-file provider as the comic service asks it.
type RecordProvider interface {
	Namespace() string
	Search(ctx context.Context, query specprovider.Query) ([]specprovider.Record, error)
	Item(ctx context.Context, id string) (specprovider.Record, error)
}

func (s Sources) recordProvider(key string) RecordProvider {
	if s.Records == nil {
		return nil
	}
	return s.Records(key)
}

// candidateFromRecord is one search result of a definition; its own id is stored under the definition's key.
func candidateFromRecord(key string, provider RecordProvider, record specprovider.Record) Candidate {
	ids := idsOfRecord(key, provider, record)
	cover := ""
	for _, image := range record.Images {
		if image.Kind == "cover" || image.Kind == "poster" || cover == "" {
			cover = image.URL
		}
	}
	return Candidate{Title: record.Title, Overview: record.Overview, Cover: cover, Year: record.Year, Count: record.PageCount, Adult: record.Adult, IDs: ids}
}

func idsOfRecord(key string, provider RecordProvider, record specprovider.Record) map[string]string {
	ids := map[string]string{}
	for namespace, id := range record.IDs {
		if namespace == provider.Namespace() {
			namespace = key
		}
		ids[namespace] = id
	}
	return ids
}

func (s Sources) searchRecords(ctx context.Context, key, query string) []Candidate {
	provider := s.recordProvider(key)
	if provider == nil {
		return nil
	}
	found, err := provider.Search(ctx, specprovider.Query{Title: query})
	if err != nil {
		s.warn(key, query, err)
	}
	var out []Candidate
	for _, record := range found {
		out = append(out, candidateFromRecord(key, provider, record))
	}
	return out
}

// askRecords reads the series from a definition: by its own id when the series has one, else the first search result whose
// title matches the hint.
func (s Sources) askRecords(ctx context.Context, series *Series, key string, hint Hint) bool {
	provider := s.recordProvider(key)
	if provider == nil {
		return false
	}
	id := series.IDs[key]
	if id == "" {
		title := firstNonEmpty(series.Title, hint.Title)
		if title == "" {
			return false
		}
		found, err := provider.Search(ctx, specprovider.Query{Title: title, Year: yearText(hint.Year)})
		if err != nil {
			s.warn(key, title, err)
			return false
		}
		for _, record := range found {
			if sameTitle(record.Title, title) && (hint.Year == 0 || record.Year == 0 || record.Year == hint.Year) {
				id = record.IDs[provider.Namespace()]
				break
			}
		}
		if id == "" {
			return false
		}
	}
	record, err := provider.Item(ctx, id)
	if err != nil {
		s.warn(key, id, err)
		series.Failed = append(series.Failed, key)
		return false
	}
	series.fromRecord(key, provider, record)
	return true
}

func yearText(year int) string {
	if year == 0 {
		return ""
	}
	return strconv.Itoa(year)
}

// fromRecord fills what the series lacks from a definition's record: a provider only fills, it never overwrites.
func (s *Series) fromRecord(key string, provider RecordProvider, record specprovider.Record) {
	for namespace, id := range idsOfRecord(key, provider, record) {
		if s.IDs[namespace] == "" {
			s.IDs[namespace] = id
		}
	}
	cover := ""
	for _, image := range record.Images {
		if image.Kind == "cover" || cover == "" {
			cover = image.URL
		}
	}
	publisher := ""
	for _, organisation := range record.Organisations {
		if publisher == "" || strings.EqualFold(organisation.Role, "publisher") {
			publisher = organisation.Name
		}
	}
	endYear := 0
	if len(record.EndDate) >= 4 {
		endYear, _ = strconv.Atoi(record.EndDate[:4])
	}
	s.fill(record.Title, record.Overview, publisher, cover, record.Year, endYear)
	s.Adult = s.Adult || record.Adult
	s.addAliases(append([]string{record.OriginalTitle}, record.AltTitles...)...)
	s.addGenres(record.Genres...)
	s.addGenres(record.Tags...)
	if s.OriginalLanguage == "" {
		s.OriginalLanguage = record.Language
	}
	if s.Status == "" {
		s.Status = record.Status
	}
	for _, person := range record.People {
		s.Credits = append(s.Credits, Credit{Name: person.Name, Role: person.Role, Order: len(s.Credits)})
	}
	var parts []Part
	for _, part := range record.Parts {
		number := strconv.FormatFloat(part.Number, 'f', -1, 64)
		kind := part.Kind
		if kind == "" {
			kind = partKind(number)
		}
		parts = append(parts, Part{Kind: kind, Number: number, Title: part.Title, CoverDate: part.Date, IDs: map[string]string{key: part.IDs["own"]}})
	}
	if len(parts) == 0 && record.PageCount > 0 && s.Format == "" {
		s.Format = FormatGallery
		parts = []Part{{Kind: PartVolume, Number: "1", Pages: record.PageCount}}
	}
	s.mergeParts(parts)
	s.Sources = append(s.Sources, key)
}
