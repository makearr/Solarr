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

// PersonIdentity is what a person should look like if one has to be created --
// naming and biographical facts only, the same "this package decides
// persistence, the caller decides what to name" split as WorkIdentity.
// The sort name is always the name, as Lidarr sorts: MusicBrainz's "Doci, Vutu" filed Vutu Doci under D.
type PersonIdentity struct {
	Name           string
	Disambiguation string
	// Kind is the provider's own vocabulary ("Person", "Group", ...), stored
	// as-is, same reasoning as ReleaseGroup.PrimaryType.
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

// ResolvePerson finds the person behind link's external id, or creates one
// with link.Person's facts when none exists yet. Unlike resolveWork, an
// existing person's facts are never overwritten by a later call -- there is
// no update path (person.fields_json aside, nothing about a person changes
// often enough to need one yet), matching findOrCreateWork's own
// once-created-stays-created shape.
func (p People) ResolvePerson(ctx context.Context, at time.Time, link PersonLink) (int64, error) {
	var personID int64
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		id, err := findOrCreatePerson(ctx, queries, at, link)
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

// FindByExternalID answers the person behind an external id, without
// creating one -- the "did this call create it or only match it" question
// ResolvePerson's own return value cannot answer, needed by a caller that
// must record which is which (todo 4.13's ArrImportRevert: only a person a
// run itself created may be undone).
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
	ctx context.Context, queries *librarydb.Queries, at time.Time, link PersonLink,
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

// AddCredit records a work-level credit (edition_id/part_id zero, Appendix E D2) for an already-resolved person; ord
// is the credit order (0-based).
func (p People) AddCredit(ctx context.Context, workID, personID int64, role string, ord int) error {
	return p.addCredit(ctx, workID, personID, role, ord, false)
}

// AddLeadCredit is AddCredit first in the media type's lead role (an album's artist): the person the work is listed
// under, kept in work.primary_artist_id and its sort name, the Tracks level's denormalised key (0021).
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
		return queries.SetWorkPrimaryArtist(ctx, librarydb.SetWorkPrimaryArtistParams{
			PrimaryArtistID:       sql.NullInt64{Int64: personID, Valid: true},
			PrimaryArtistSortName: sql.NullString{String: person.SortName, Valid: true},
			ID:                    workID,
		})
	})
	if err != nil {
		return fmt.Errorf("library: add credit (person %d, work %d, role %s): %w", personID, workID, role, err)
	}
	return nil
}

// AssignGenres replaces a work's musicbrainz-sourced genre tags with the given
// set: existing musicbrainz assignments are cleared first, so a release that
// lost a genre on MusicBrainz loses it here too, while any tag a user added
// by hand (a different source) is never touched (ADR-33).
func (p People) AssignGenres(ctx context.Context, workID int64, genres []GenreLink) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.ClearProviderTagAssignments(ctx, librarydb.ClearProviderTagAssignmentsParams{
			SubjectType: "work", SubjectID: workID, Source: providerSourceMusicBrainz,
		}); err != nil {
			return fmt.Errorf("clear existing: %w", err)
		}
		for _, genre := range genres {
			tagID, err := findOrCreateTag(ctx, queries, genre.Name, providerSourceMusicBrainz)
			if err != nil {
				return fmt.Errorf("tag %q: %w", genre.Name, err)
			}
			if err := queries.AssignTag(ctx, librarydb.AssignTagParams{
				TagID: tagID, SubjectType: "work", SubjectID: workID, Source: providerSourceMusicBrainz,
			}); err != nil {
				return fmt.Errorf("assign tag %q: %w", genre.Name, err)
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("library: assign genres to work %d: %w", workID, err)
	}
	return nil
}

// providerSourceMusicBrainz is the tag_assignment.source value every genre
// this package writes uses -- a refresh only ever clears its own source's
// rows (ADR-33), so this string is the boundary of what a refresh may touch.
const providerSourceMusicBrainz = "musicbrainz"

func findOrCreateTag(ctx context.Context, queries *librarydb.Queries, name, source string) (int64, error) {
	slug := slugify(name)
	existing, err := queries.GetTagBySlug(ctx, slug)
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up: %w", err)
	}

	tag, err := queries.CreateTag(ctx, librarydb.CreateTagParams{
		Name: name, Slug: slug, Source: source,
	})
	if err != nil {
		return 0, fmt.Errorf("create: %w", err)
	}
	// Flat, no parent: every tag is still its own ancestor at depth zero, or
	// ListSubjectsForTagTree would never match it (tag_closure's own doc
	// comment in the migration).
	if err := queries.AddTagSelfClosure(ctx, librarydb.AddTagSelfClosureParams{
		AncestorID: tag.ID, DescendantID: tag.ID,
	}); err != nil {
		return 0, fmt.Errorf("self closure: %w", err)
	}
	return tag.ID, nil
}

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

// DiscographyRow is one work credited to a person, plus the availability
// signal the artist page needs (Lidarr-like "available / missing /
// requested", user request 2026-09-12) -- the same wanted-state and on-disk
// facts WorkListRow already carries for the library list, composed the same
// way (raw SQL, DiscographyRow's own doc comment on why).
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

// Discography pages every work credited to a person, any role when role is
// empty, oldest release first. Raw SQL, not sqlc, for the same reason
// works.go's List is: wanted_state is a correlated scalar subquery sqlc
// cannot see is nullable the way it can a real table column, and would type
// it as a plain non-null string -- a panic waiting on the first row with no
// monitored desired_item.
func (p People) Discography(ctx context.Context, personID int64, role string) ([]DiscographyRow, error) {
	query := `SELECT ` + workColumns + `, work.primary_type,
       ` + workStateColumn + `,
       CAST((SELECT COUNT(*) FROM work_root_folder wrf WHERE wrf.work_id = work.id) AS INTEGER) AS on_disk_count,
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
			&row.PrimaryType, &row.WantedState, &row.OnDiskCount,
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
	// FiltersJSON is opaque here (subscription.FiltersJSON's own doc comment):
	// which vocabulary it filters on is a media type's own concern (ADR-23).
	// Empty means "use that media type's own default" -- never written as an
	// empty JSON object, so a caller that never sets this changes nothing.
	FiltersJSON string
	// CategoryID is the library this artist was added to, so the expansion
	// puts their albums there rather than in whichever library of that media
	// type happens to come first (2026-09-19). 0 leaves it unset, which the
	// expansion reads as "the media type's own first library", the behaviour
	// every subscription made before this had.
	CategoryID int64
}

// Subscribe records a subscription and marks the person monitored, in one
// transaction -- ADR-32: "adding a performer monitors their scenes regardless
// of studio". CategoryID records which library the artist was added to;
// ProfileID stays unset, resolved from that category when the expansion runs.
//
// Idempotent on (person, user): a second call for the same pair returns the
// existing subscription's id rather than erroring on the UNIQUE(subject_type,
// subject_id, user_id) constraint or creating a duplicate -- Invariant 80,
// the same "existing facts never overwritten" shape ResolvePerson's own doc
// comment already states for a person. Found needed live: SubscribePerson's
// own API handler had no other guard against a double-subscribe before this.
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

// Delete removes a person outright, cascading person_alias, credit and
// person_external_id (every REFERENCES person(id) with ON DELETE CASCADE).
// Revert-only use so far (todo 4.13's ArrImportRevert): only ever called for
// a person this package itself created (arr_import_item.person_created), and
// only after the person's own subscription (no cascade of its own,
// subscription.subject_id is a polymorphic reference with no FK) and any
// work crediting them as primary_artist_id (also no cascade) are already
// gone, or this fails on the same foreign key work.primary_artist_id
// enforces.
func (p People) Delete(ctx context.Context, personID int64) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeletePerson(ctx, personID)
	})
	if err != nil {
		return fmt.Errorf("library: delete person %d: %w", personID, err)
	}
	return nil
}

// biographyFromFields decodes fields_json back out, empty when there is none
// or it does not decode -- a row this package itself wrote should always
// decode, so a failure here is treated as "no biography" rather than a hard
// error, the same reasoning musicbrainz.Provider's own cache uses.
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
