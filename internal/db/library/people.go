package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// PersonIdentity is the naming and biographical facts for a person that must be created; the sort name is always the name, as Lidarr sorts, so a comma form does not file under the surname.
type PersonIdentity struct {
	Name           string
	Disambiguation string
	// Kind is the provider's own genre tree ("Person", "Group", ...), stored
	// as-is, same reasoning as ReleaseGroup.Subtype.
	Kind    string
	Gender  string
	Country string
	// Born and Died are partial dates ("1958", "1958-07", "1958-07-30").
	Born string
	Died string
	// Biography is free text (a Wikidata-sourced extract, say) or empty when
	// none is available -- stored in fields_json, the schema's own extension
	// point, since person carries no dedicated biography column.
	Biography string
	// Aliases are the provider's other names for the person; only BackfillDetails stores them.
	Aliases []string
	// ImageURL is the provider's picture of the person, kept in fields_json.
	ImageURL string
}

// PersonLink is one external id to resolve to a person.
type PersonLink struct {
	Provider   string
	ExternalID string
	Person     PersonIdentity
}

// GenreLink is one release group's genre tag, with the provider's own vote
// count as a display hint, not stored as ranking (ADR-33's tag model has no
// per-assignment weight).
type GenreLink struct{ Name string }

// People resolves persons, credits and provider-sourced tags (ADR-32, ADR-33).
type People struct{ store *store.Store }

// NewPeople builds it over an open library store.
func NewPeople(opened *store.Store) People { return People{store: opened} }

// ResolvePerson finds the person behind link's external id or creates one; an existing person's facts are never overwritten, as with findOrCreateWork.
func (p People) ResolvePerson(ctx context.Context, at time.Time, link PersonLink) (int64, error) {
	var personID int64
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		id, err := findOrCreatePerson(ctx, tx, queries, at, link)
		if err != nil {
			return err
		}
		personID = id
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: resolve person %s:%s: %w", link.Provider, link.ExternalID, err)
	}
	return personID, nil
}

// BackfillDetails corrects the name and sort name to the provider's and fills only the empty
// biographical columns: a person first made from a release's credit name ("VUTU") gets the artist's own.
func (p People) BackfillDetails(ctx context.Context, personID int64, identity PersonIdentity) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		for _, alias := range identity.Aliases {
			if strings.TrimSpace(alias) == "" {
				continue
			}
			if err := queries.AddPersonAlias(ctx, librarydb.AddPersonAliasParams{
				PersonID: personID, Alias: alias, Normalized: normalizedAlias(alias), Source: "musicbrainz",
			}); err != nil {
				return err
			}
		}
		return queries.BackfillPersonDetails(ctx, librarydb.BackfillPersonDetailsParams{
			ID:             personID,
			Name:           identity.Name,
			SortName:       identity.Name,
			Disambiguation: nullString(identity.Disambiguation),
			Kind:           nullString(identity.Kind),
			Gender:         nullString(identity.Gender),
			Country:        nullString(identity.Country),
			Born:           nullString(identity.Born),
			Died:           nullString(identity.Died),
			FieldsJson:     nullString(personFieldsJSON(identity)),
		})
	})
}

// SetBiography stores a provider's biography for a person who has none.
func (p People) SetBiography(ctx context.Context, personID int64, biography string) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetPersonBiography(ctx, librarydb.SetPersonBiographyParams{Biography: biography, ID: personID})
	})
}

// ResolveFolderArtist is the artist of an album known only by its folder (D6): the one provider-identified
// person of that name when there is exactly one, else a local person keyed by the name.
func (p People) ResolveFolderArtist(ctx context.Context, at time.Time, provider, name string) (int64, error) {
	queries := librarydb.New(p.store.Read())
	matches, err := queries.FindIdentifiedPeopleByName(ctx, name)
	if err == nil && len(matches) == 0 {
		matches, err = queries.FindIdentifiedPeopleByAlias(ctx, name)
	}
	if err != nil {
		return 0, fmt.Errorf("library: find people named %q: %w", name, err)
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return p.ResolvePerson(ctx, at, PersonLink{
		Provider: provider, ExternalID: strings.ToLower(name), Person: PersonIdentity{Name: name},
	})
}

// normalizedAlias is the lookup form of an alias: trimmed and lower-cased.
func normalizedAlias(alias string) string { return strings.ToLower(strings.TrimSpace(alias)) }

// FindByExternalID answers the person behind an external id without creating one, for callers recording created versus matched (todo 4.13 ArrImportRevert).
func (p People) FindByExternalID(ctx context.Context, provider, externalID string) (int64, bool, error) {
	person, err := librarydb.New(p.store.Read()).GetPersonByExternalID(ctx, librarydb.GetPersonByExternalIDParams{
		Provider: provider, ExternalID: externalID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("library: find person %s:%s: %w", provider, externalID, err)
	}
	return person.ID, true, nil
}

func findOrCreatePerson(
	ctx context.Context, tx librarydb.DBTX, queries *librarydb.Queries, at time.Time, link PersonLink,
) (int64, error) {
	existing, err := queries.GetPersonByExternalID(ctx, librarydb.GetPersonByExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID,
	})
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up: %w", err)
	}

	if joined, found, err := joinSameNamed(ctx, tx, queries, link); err != nil {
		return 0, err
	} else if found {
		return joined, nil
	}

	identity := link.Person
	person, err := queries.CreatePerson(ctx, librarydb.CreatePersonParams{
		Name: identity.Name, SortName: identity.Name,
		Disambiguation: nullString(identity.Disambiguation), Kind: nullString(identity.Kind),
		Gender: nullString(identity.Gender), Country: nullString(identity.Country),
		Born: nullString(identity.Born), Died: nullString(identity.Died),
		FieldsJson: nullString(personFieldsJSON(identity)),
		Monitored:  0, AddedAt: at.Unix(),
	})
	if err != nil {
		return 0, fmt.Errorf("create person: %w", err)
	}
	if err := queries.LinkPersonExternalID(ctx, librarydb.LinkPersonExternalIDParams{
		Provider: link.Provider, ExternalID: link.ExternalID, PersonID: person.ID,
	}); err != nil {
		return 0, fmt.Errorf("link external id: %w", err)
	}
	return person.ID, nil
}

// joinSameNamed links the id to the one identified person it names: by name, else by alias, whose birth does not
// contradict the new person's and who holds no id of the link's provider; several candidates stay apart.
func joinSameNamed(ctx context.Context, tx librarydb.DBTX, queries *librarydb.Queries, link PersonLink) (int64, bool, error) {
	name := strings.TrimSpace(link.Person.Name)
	if name == "" || strings.HasPrefix(link.Provider, "local:") {
		return 0, false, nil
	}
	for _, lookup := range []string{
		`SELECT p.id, COALESCE(p.born, '') AS born FROM person p WHERE p.name = ? COLLATE NOCASE`,
		`SELECT p.id, COALESCE(p.born, '') AS born FROM person p WHERE p.id IN (SELECT person_id FROM person_alias WHERE normalized = ?)`,
	} {
		arg := name
		if strings.Contains(lookup, "normalized") {
			arg = normalizedAlias(name)
		}
		matches, err := identifiedMatches(ctx, tx, lookup, arg, link)
		if err != nil {
			return 0, false, err
		}
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 1 {
			return 0, false, nil
		}
		if err := queries.LinkPersonExternalID(ctx, librarydb.LinkPersonExternalIDParams{
			Provider: link.Provider, ExternalID: link.ExternalID, PersonID: matches[0],
		}); err != nil {
			return 0, false, fmt.Errorf("link external id: %w", err)
		}
		return matches[0], true, nil
	}
	return 0, false, nil
}

// identifiedMatches runs a person lookup and keeps who is identified by a provider, holds no id of the link's and was
// not born at another time.
func identifiedMatches(ctx context.Context, tx librarydb.DBTX, lookup, arg string, link PersonLink) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, `SELECT found.id, found.born FROM (`+lookup+`) found
WHERE EXISTS (SELECT 1 FROM person_external_id y WHERE y.person_id = found.id AND y.provider NOT LIKE 'local:%')
  AND NOT EXISTS (SELECT 1 FROM person_external_id x WHERE x.person_id = found.id AND x.provider = ?)
ORDER BY found.id LIMIT 3`, arg, link.Provider)
	if err != nil {
		return nil, fmt.Errorf("join same-named: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var matches []int64
	for rows.Next() {
		var id int64
		var born string
		if err := rows.Scan(&id, &born); err != nil {
			return nil, err
		}
		if bornCompatible(born, link.Person.Born) {
			matches = append(matches, id)
		}
	}
	return matches, rows.Err()
}

// bornCompatible is false only when both births are known and differ on the digits both give.
func bornCompatible(a, b string) bool {
	if a == "" || b == "" {
		return true
	}
	shared := min(len(a), len(b), 10)
	return a[:shared] == b[:shared]
}

// AddCredit records a work-level credit (edition_id/part_id zero, Appendix E D2) for an already-resolved person; ord
// is the credit order (0-based).
func (p People) AddCredit(ctx context.Context, workID, personID int64, role string, ord int) error {
	return p.addCredit(ctx, workID, personID, role, ord, false)
}

// AddLeadCredit is AddCredit first in the media type's lead role (an album's artist): the person the work is listed
// under, kept in work.lead_person_id and its sort name, the Tracks level's denormalised key (0021).
func (p People) AddLeadCredit(ctx context.Context, workID, personID int64, role string) error {
	return p.addCredit(ctx, workID, personID, role, 0, true)
}

func (p People) addCredit(ctx context.Context, workID, personID int64, role string, ord int, lead bool) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: personID, WorkID: workID, EditionID: 0, PartID: 0,
			Role: role, Character: "", Ord: sql.NullInt64{Int64: int64(ord), Valid: true},
		}); err != nil {
			return err
		}
		if err := refreshPeople(ctx, queries, []int64{personID}); err != nil {
			return err
		}
		if !lead {
			return nil
		}
		person, err := queries.GetPerson(ctx, personID)
		if err != nil {
			return fmt.Errorf("primary artist: get person: %w", err)
		}
		return queries.SetWorkLeadPerson(ctx, librarydb.SetWorkLeadPersonParams{
			LeadPersonID:       sql.NullInt64{Int64: personID, Valid: true},
			LeadPersonSortName: sql.NullString{String: person.SortName, Valid: true},
			ID:                 workID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: add credit (person %d, work %d, role %s): %w", personID, workID, role, err)
	}
	return nil
}

// SetRating records one provider's community rating of a work, normalised to 0..10, replacing that provider's earlier one.
func (p People) SetRating(ctx context.Context, workID int64, provider string, score float64, votes int64, at int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetWorkRating(ctx, librarydb.SetWorkRatingParams{
			WorkID: workID, Provider: provider, Score: score, Votes: sql.NullInt64{Int64: votes, Valid: votes > 0}, FetchedAt: at,
		})
	})
	if err != nil {
		return fmt.Errorf("library: set %s rating of work %d: %w", provider, workID, err)
	}
	return nil
}

// SetAliases records the other titles a provider knows a work by, one per line, replacing that provider's earlier list.
func (p People) SetAliases(ctx context.Context, workID int64, provider string, aliases []string, at int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetWorkField(ctx, librarydb.SetWorkFieldParams{
			WorkID: workID, Field: WorkFieldAliases, Provider: provider,
			Value: sql.NullString{String: strings.Join(aliases, "\n"), Valid: true}, FetchedAt: at,
		})
	})
	if err != nil {
		return fmt.Errorf("library: set %s aliases of work %d: %w", provider, workID, err)
	}
	return nil
}

// WorkFieldAliases is the work_field name SetAliases writes.
const WorkFieldAliases = "aliases"

// AssignGenres replaces a work's provider genres of the genre tree with MusicBrainz's; a person's own stay (ADR-33).
func (p People) AssignGenres(ctx context.Context, workID int64, genreTree string, genres []GenreLink) error {
	return p.AssignGenresFrom(ctx, workID, genreTree, providerSourceMusicBrainz, genres)
}

// GenreSourceWikidata is the source of the genres Wikidata names, which replace MusicBrainz's where it has any.
const GenreSourceWikidata = "wikidata"

// GenreSourceArtist is the source of the genres an album takes from its artist while MusicBrainz has none of its own for it.
const GenreSourceArtist = "musicbrainz-artist"

// ClearGenresFrom removes the genres one source gave a work, and no one else's.
func (p People) ClearGenresFrom(ctx context.Context, workID int64, source string) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).ClearProviderTagAssignments(ctx, librarydb.ClearProviderTagAssignmentsParams{SubjectType: "work", SubjectID: workID, Source: source})
	})
	if err != nil {
		return fmt.Errorf("library: clear %s genres of work %d: %w", source, workID, err)
	}
	return nil
}

// AssignGenresFrom replaces a work's provider genres of the genre tree with one provider's, mapped onto its tree.
func (p People) AssignGenresFrom(ctx context.Context, workID int64, genreTree, source string, genres []GenreLink) error {
	names := make([]string, len(genres))
	for i, genre := range genres {
		names[i] = genre.Name
	}
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return assignGenres(ctx, librarydb.New(tx), workID, genreTree, source, names, false)
	})
	if err != nil {
		return fmt.Errorf("library: assign genres to work %d: %w", workID, err)
	}
	return nil
}

// providerSourceMusicBrainz is the tag_assignment.source of MusicBrainz's genres.
const providerSourceMusicBrainz = GenreSourceMusicBrainz

// GenreSourceMusicBrainz is the source of the genres MusicBrainz voted onto a release group.
const GenreSourceMusicBrainz = "musicbrainz"

// slugify turns a genre name into tag.slug's globally-unique key: lowercase,
// non-alphanumeric runs collapsed to a single hyphen.
func slugify(name string) string {
	var b strings.Builder
	lastWasHyphen := true // leading hyphen suppressed the same way a repeat is
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			lastWasHyphen = false
		case !lastWasHyphen:
			b.WriteByte('-')
			lastWasHyphen = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// DiscographyRow is one work credited to a person plus the available/missing/requested signal the artist page needs, composed in raw SQL like WorkListRow.
type DiscographyRow struct {
	librarydb.Work
	WantedState sql.NullString
	OnDiskCount int64
	// PresentCount and RequiredCount are the album's own "9 of 12", for the
	// availability bar under its cover (2026-09-19).
	PresentCount  sql.NullInt64
	RequiredCount sql.NullInt64
	// AvailableFrom is the release date the planner holds the item until,
	// which is what makes an album unreleased rather than merely missing.
	AvailableFrom sql.NullInt64
	// Downloading reports an acquisition in flight for this album.
	Downloading bool
	// Unverified is an album known only by its own tags or folder (D6).
	Unverified bool
}

// discographyStatusColumns are the four facts the artist page colours a row
// by, each scalar against the same monitored desired_item workStateColumn
// picks, for the same reason it is a subquery rather than a join.
const discographyStatusColumns = `(SELECT di.present_count FROM desired_item di
      WHERE di.work_id = work.id AND di.monitored = 1
      ORDER BY di.satisfaction_kind = 'SET' DESC, di.id LIMIT 1) AS present_count,
   (SELECT di.required_count FROM desired_item di
      WHERE di.work_id = work.id AND di.monitored = 1
      ORDER BY di.satisfaction_kind = 'SET' DESC, di.id LIMIT 1) AS required_count,
   (SELECT di.available_from FROM desired_item di
      WHERE di.work_id = work.id AND di.monitored = 1
      ORDER BY di.satisfaction_kind = 'SET' DESC, di.id LIMIT 1) AS available_from,
   EXISTS (SELECT 1 FROM acquisition a JOIN desired_item di ON di.id = a.desired_item_id
      WHERE di.work_id = work.id
        AND a.state NOT IN ('IMPORTED', 'CLEANED', 'FAILED', 'CANCELLED')) AS downloading`

// Discography pages works credited to a person (any role if empty), oldest first; raw SQL because sqlc would type the wanted_state subquery as non-null and panic on a work with no monitored item.
func (p People) Discography(ctx context.Context, personID int64, role string) ([]DiscographyRow, error) {
	query := `SELECT ` + workColumns + `, work.subtype,
       ` + workStateColumn + `,
       CAST(EXISTS (SELECT 1 FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id WHERE wf.work_id = work.id AND lp.missing_since IS NULL) AS INTEGER) AS on_disk_count,
       ` + discographyStatusColumns + `,
       ` + unverifiedColumn + ` AS unverified
FROM work
JOIN credit c ON c.work_id = work.id
WHERE c.person_id = ? AND (? = '' OR c.role = ?)
ORDER BY work.year, work.sort_title, work.id`

	rows, err := p.store.Read().QueryContext(ctx, query, personID, role, role)
	if err != nil {
		return nil, fmt.Errorf("library: discography: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var discography []DiscographyRow
	for rows.Next() {
		var row DiscographyRow
		if err := rows.Scan(&row.ID, &row.MediaType, &row.CanonicalTitle, &row.SortTitle,
			&row.OriginalLanguage, &row.Year, &row.RuntimeMs, &row.AddedAt, &row.UpdatedAt, &row.AddedBy,
			&row.Subtype, &row.WantedState, &row.OnDiskCount,
			&row.PresentCount, &row.RequiredCount, &row.AvailableFrom, &row.Downloading, &row.Unverified); err != nil {
			return nil, fmt.Errorf("library: scan discography row: %w", err)
		}
		discography = append(discography, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: discography: %w", err)
	}
	return discography, nil
}

// RecordRefresh remembers when an artist's metadata was last refreshed.
func (p People) RecordRefresh(ctx context.Context, personID, at int64) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordPersonRefresh(ctx, librarydb.RecordPersonRefreshParams{PersonID: personID, RefreshedAt: at})
	})
}

// ArtistsDueRefresh answers up to limit monitored artists not refreshed since before, stalest first.
func (p People) ArtistsDueRefresh(ctx context.Context, before, limit int64) ([]int64, error) {
	ids, err := librarydb.New(p.store.Read()).ListArtistsDueRefresh(ctx, librarydb.ListArtistsDueRefreshParams{
		Before: before, PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("library: artists due refresh: %w", err)
	}
	return ids, nil
}

// ArtistsWithFreshRelease answers the monitored artists with a wanted album released within the fresh window of now.
func (p People) ArtistsWithFreshRelease(ctx context.Context, now time.Time) (map[int64]bool, error) {
	ids, err := librarydb.New(p.store.Read()).ListArtistsWithFreshRelease(ctx, librarydb.ListArtistsWithFreshReleaseParams{
		ReleasedFrom: sql.NullInt64{Int64: now.Add(-FreshAired).Unix(), Valid: true}, ReleasedUntil: sql.NullInt64{Int64: now.Add(FreshAhead).Unix(), Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("library: artists with a fresh release: %w", err)
	}
	fresh := make(map[int64]bool, len(ids))
	for _, id := range ids {
		fresh[id] = true
	}
	return fresh, nil
}

// Person reads one person's own row, without Detail's discography.
func (p People) Person(ctx context.Context, id int64) (librarydb.Person, error) {
	person, err := librarydb.New(p.store.Read()).GetPerson(ctx, id)
	if err != nil {
		return librarydb.Person{}, fmt.Errorf("library: person %d: %w", id, err)
	}
	return person, nil
}

// SubscriptionRequest is what a caller supplies to subscribe to a person
// (ADR-32): media types x roles, optionally the whole back catalogue.
type SubscriptionRequest struct {
	MediaTypes           []string
	Roles                []string
	MonitorBackCatalogue bool
	// FiltersJSON is opaque here; which genre tree it filters is the media type's concern (ADR-23), and empty means its default, never an empty object.
	FiltersJSON string
	// CategoryID is the library the artist was added to so the expansion puts albums there; 0 means the media type's first library.
	CategoryID int64
}

// Subscribe records a subscription and marks the person monitored in one transaction (ADR-32); idempotent on (person, user) so a double subscribe returns the existing id instead of failing the UNIQUE constraint (Invariant 80).
func (p People) Subscribe(
	ctx context.Context, at time.Time, personID, userID int64, request SubscriptionRequest,
) (int64, error) {
	mediaType := ""
	if len(request.MediaTypes) > 0 {
		mediaType = request.MediaTypes[0]
	}
	var subscriptionID int64
	err := p.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		// subject_id has no FK (person, organisation and tag share it), so a missing person is checked here.
		if _, err := queries.GetPerson(ctx, personID); err != nil {
			return err
		}
		followed, err := upsertFollow(ctx, queries, at, Subject{Type: "person", ID: personID}, userID, FollowRequest{
			CategoryID: request.CategoryID, MediaType: mediaType, Roles: request.Roles, FiltersJSON: request.FiltersJSON,
			BackCatalogue: request.MonitorBackCatalogue,
		}, false)
		subscriptionID = followed.ID
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("library: subscribe person %d: %w", personID, err)
	}
	return subscriptionID, nil
}

// Delete removes a person, cascading aliases, credits and external ids; used only by ArrImportRevert for persons it created, after the subscription and any lead_person_id credits are gone (neither has a cascade).
func (p People) Delete(ctx context.Context, personID int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeletePerson(ctx, personID)
	})
	if err != nil {
		return fmt.Errorf("library: delete person %d: %w", personID, err)
	}
	return nil
}

// biographyFromFields decodes fields_json, treating a failure as no biography rather than a hard error.
func biographyFromFields(raw sql.NullString) string {
	if !raw.Valid {
		return ""
	}
	var fields personFields
	if json.Unmarshal([]byte(raw.String), &fields) != nil {
		return ""
	}
	return fields.Biography
}

// personFields is fields_json's shape -- extension data with no dedicated
// column (ADR-23's pattern), currently just the one field a biography needs.
type personFields struct {
	Biography string `json:"biography,omitempty"`
	ImageURL  string `json:"image_url,omitempty"`
}

// personFieldsJSON is empty when identity has nothing worth recording, so a
// person resolved with no biography gets a NULL fields_json rather than "{}".
func personFieldsJSON(identity PersonIdentity) string {
	if identity.Biography == "" && identity.ImageURL == "" {
		return ""
	}
	encoded, err := json.Marshal(personFields{Biography: identity.Biography, ImageURL: identity.ImageURL})
	if err != nil {
		return ""
	}
	return string(encoded)
}
