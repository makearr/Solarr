package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// PersonProfile is one person's own fields, as every library shows them.
type PersonProfile struct {
	ID             int64
	Name           string
	SortName       string
	Disambiguation string
	Kind           string
	Gender         string
	Country        string
	Born           string
	Died           string
	ImageURL       string
	Biography      string
	// Extras are the media type's own facts, links and further pictures of the person.
	Extras PersonExtras
	// Attributes are the provider's and the person's own, merged.
	Attributes PersonAttributes
	Monitored  bool
	AddedAt    int64
	Aliases    []string
	// IDs maps provider to the person's id there.
	IDs map[string]string
	// Genres are tagged on any of their works; a person carries none of their own (ADR-32).
	Genres []string
	// GenreTags are the same genres with the tag each is, for a link to everything carrying it.
	GenreTags []PersonGenre
	// Libraries are where the viewer finds them, by role.
	Libraries []PersonLibrary
}

// PersonGenre is a genre tagged on a person's works.
type PersonGenre struct {
	TagID int64
	Name  string
}

// PersonLibrary is a person's place in one library: a role and how many works they hold it on.
type PersonLibrary struct {
	CategoryID int64
	Role       string
	Works      int64
}

// PersonCredit is one thing a person did on one work of one library; Parts are the parts of a part-scoped role.
type PersonCredit struct {
	CategoryID int64
	Role       string
	Character  string
	Parts      []string
	DiscographyRow
}

// Get answers one person as the viewer may see them; ErrPersonNotFound when no library they may read knows of them.
func (p People) Get(ctx context.Context, id int64, visible []int64) (PersonProfile, error) {
	reader := p.store.Read()
	rule := visiblePerson("person.id", visible)
	var seen int
	if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM person WHERE person.id = ? AND "+rule.sql,
		append([]any{id}, rule.args...)...).Scan(&seen); err != nil {
		return PersonProfile{}, fmt.Errorf("library: person %d: %w", id, err)
	}
	if seen == 0 {
		return PersonProfile{}, ErrPersonNotFound
	}
	queries := librarydb.New(reader)
	person, err := queries.GetPerson(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return PersonProfile{}, ErrPersonNotFound
	}
	if err != nil {
		return PersonProfile{}, fmt.Errorf("library: person %d: %w", id, err)
	}
	profile := PersonProfile{
		ID: person.ID, Name: person.Name, SortName: person.SortName, Disambiguation: person.Disambiguation.String,
		Kind: person.Kind.String, Gender: person.Gender.String, Country: person.Country.String,
		Born: person.Born.String, Died: person.Died.String, Monitored: person.Monitored == 1, AddedAt: person.AddedAt,
		ImageURL: imageFromFields(person.FieldsJson), Biography: biographyFromFields(person.FieldsJson),
		Extras: extrasFromFields(person.FieldsJson), Attributes: attributesFromFields(person.FieldsJson, person.Died.String),
	}
	if profile.Aliases, err = queries.ListAliasesForPerson(ctx, id); err != nil {
		return PersonProfile{}, fmt.Errorf("library: person %d aliases: %w", id, err)
	}
	genres, err := queries.ListGenresForPersonDiscography(ctx, id)
	if err != nil {
		return PersonProfile{}, fmt.Errorf("library: person %d genres: %w", id, err)
	}
	for _, genre := range genres {
		profile.Genres = append(profile.Genres, genre.TagName)
		profile.GenreTags = append(profile.GenreTags, PersonGenre{TagID: genre.TagID, Name: genre.TagName})
	}
	externals, err := queries.ListPersonExternalIDs(ctx, id)
	if err != nil {
		return PersonProfile{}, fmt.Errorf("library: person %d ids: %w", id, err)
	}
	profile.IDs = make(map[string]string, len(externals))
	for _, external := range externals {
		profile.IDs[external.Provider] = external.ExternalID
	}
	scope := inScope("category_id", visible)
	rows, err := reader.QueryContext(ctx,
		"SELECT category_id, role, works FROM person_library WHERE person_id = ? AND role <> '' AND "+scope.sql+
			" ORDER BY category_id, works DESC, role", append([]any{id}, scope.args...)...)
	if err != nil {
		return PersonProfile{}, fmt.Errorf("library: person %d libraries: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var place PersonLibrary
		if err := rows.Scan(&place.CategoryID, &place.Role, &place.Works); err != nil {
			return PersonProfile{}, fmt.Errorf("library: person %d libraries: %w", id, err)
		}
		profile.Libraries = append(profile.Libraries, place)
	}
	return profile, rows.Err()
}

// Credits answers what a person did in the libraries the viewer may read: by library, role, then oldest work first.
func (p People) Credits(ctx context.Context, id int64, visible []int64) ([]PersonCredit, error) {
	scope := inScope("d.category_id", visible)
	query := `SELECT DISTINCT d.category_id, c.role, c.character, COALESCE(pt.media_ref, ''), COALESCE(pt.sort_key, ''),
       ` + workColumns + `, work.subtype,
       ` + workStateColumn + `,
       CAST(EXISTS (SELECT 1 FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id WHERE wf.work_id = work.id AND lp.missing_since IS NULL) AS INTEGER) AS on_disk_count,
       ` + discographyStatusColumns + `,
       ` + unverifiedColumn + ` AS unverified
FROM credit c
JOIN work ON work.id = c.work_id
JOIN desired_item d ON d.work_id = c.work_id AND ` + scope.sql + `
LEFT JOIN work_part pt ON pt.id = c.part_id AND c.part_id != 0
WHERE c.person_id = ?
ORDER BY d.category_id, c.role, work.year, work.sort_title, work.id, c.character, COALESCE(pt.sort_key, '')`
	rows, err := p.store.Read().QueryContext(ctx, query, append(slices.Clone(scope.args), id)...)
	if err != nil {
		return nil, fmt.Errorf("library: person %d credits: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	var credits []PersonCredit
	for rows.Next() {
		var credit PersonCredit
		var part, sortKey string
		row := &credit.DiscographyRow
		if err := rows.Scan(&credit.CategoryID, &credit.Role, &credit.Character, &part, &sortKey,
			&row.ID, &row.MediaType, &row.CanonicalTitle, &row.SortTitle, &row.OriginalLanguage, &row.Year, &row.RuntimeMs,
			&row.AddedAt, &row.UpdatedAt, &row.AddedBy, &row.Subtype, &row.WantedState, &row.OnDiskCount,
			&row.PresentCount, &row.RequiredCount, &row.AvailableFrom, &row.Downloading, &row.Unverified); err != nil {
			return nil, fmt.Errorf("library: scan person %d credit: %w", id, err)
		}
		last := len(credits) - 1
		if last >= 0 && credits[last].CategoryID == credit.CategoryID && credits[last].ID == credit.ID &&
			credits[last].Role == credit.Role && credits[last].Character == credit.Character {
			if part != "" {
				credits[last].Parts = append(credits[last].Parts, part)
			}
			continue
		}
		if part != "" {
			credit.Parts = []string{part}
		}
		credits = append(credits, credit)
	}
	return credits, rows.Err()
}

// workImage is one picture a provider holds for a work, as the "images" field stores them.
type workImage struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// Posters answers each work's poster from its stored provider pictures, the user's own choice first; a work with
// none is left out.
func (w Works) Posters(ctx context.Context, workIDs []int64) (map[int64]string, error) {
	posters := make(map[int64]string, len(workIDs))
	for _, id := range unique(workIDs) {
		pictures, err := w.Pictures(ctx, id)
		if err != nil {
			return nil, err
		}
		if poster, ok := pictures["poster"]; ok {
			posters[id] = poster
		}
	}
	return posters, nil
}

// Pictures answers a work's stored provider pictures, the first of each kind (poster, fanart, logo).
func (w Works) Pictures(ctx context.Context, workID int64) (map[string]string, error) {
	fields, err := librarydb.New(w.store.Read()).ListWorkFields(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: pictures of work %d: %w", workID, err)
	}
	pictures := map[string]string{}
	var images []workImage
	if raw := mergeFields(fields)["images"]; raw == "" || json.Unmarshal([]byte(raw), &images) != nil {
		return pictures, nil //nolint:nilerr // A work with no pictures, or unreadable ones, shows none.
	}
	for _, image := range images {
		if _, have := pictures[image.Kind]; !have && image.URL != "" {
			pictures[image.Kind] = image.URL
		}
	}
	return pictures, nil
}
