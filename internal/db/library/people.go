package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// PersonIdentity is what a person should look like if one has to be created --
// naming and biographical facts only, the same "this package decides
// persistence, the caller decides what to name" split as WorkIdentity.
type PersonIdentity struct {
	Name           string
	SortName       string
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
		Name: identity.Name, SortName: identity.SortName,
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

// creditRoleArtist mirrors music.CreditRoleArtist -- internal/db/library cannot
// import internal/media/music (the dependency arrow points the other way), and
// workArtistColumn already hardcodes this same string for the same reason.
const creditRoleArtist = "artist"

// AddCredit records a work-level credit (edition_id/part_id zero, Appendix E
// D2) for an already-resolved person. ord is the credit order (0-based). A
// role="artist" ord=0 credit -- the primary artist -- also refreshes
// work.primary_artist_id/primary_artist_sort_name in the same transaction:
// the Tracks navigation level's own denormalized sort key
// (0021_work_primary_artist.sql), and AddCredit is its one writer.
func (p People) AddCredit(ctx context.Context, workID, personID int64, role string, ord int) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		if err := queries.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: personID, WorkID: workID, EditionID: 0, PartID: 0,
			Role: role, Character: "", Ord: sql.NullInt64{Int64: int64(ord), Valid: true},
		}); err != nil {
			return err
		}
		if role != creditRoleArtist || ord != 0 {
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
			tagID, err := findOrCreateTag(ctx, queries, genre.Name)
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

func findOrCreateTag(ctx context.Context, queries *librarydb.Queries, name string) (int64, error) {
	slug := slugify(name)
	existing, err := queries.GetTagBySlug(ctx, slug)
	if err == nil {
		return existing.ID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("look up: %w", err)
	}

	tag, err := queries.CreateTag(ctx, librarydb.CreateTagParams{
		Name: name, Slug: slug, Source: providerSourceMusicBrainz,
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
}

// Discography pages every work credited to a person, any role when role is
// empty, oldest release first. Raw SQL, not sqlc, for the same reason
// works.go's List is: wanted_state is a correlated scalar subquery sqlc
// cannot see is nullable the way it can a real table column, and would type
// it as a plain non-null string -- a panic waiting on the first row with no
// monitored desired_item.
func (p People) Discography(ctx context.Context, personID int64, role string) ([]DiscographyRow, error) {
	query := `SELECT ` + workColumns + `, work.primary_type,
       ` + workStateColumn + `,
       CAST((SELECT COUNT(*) FROM work_root_folder wrf WHERE wrf.work_id = work.id) AS INTEGER) AS on_disk_count
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
			&row.PrimaryType, &row.WantedState, &row.OnDiskCount); err != nil {
			return nil, fmt.Errorf("library: scan discography row: %w", err)
		}
		discography = append(discography, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("library: discography: %w", err)
	}
	return discography, nil
}

// PersonDetail is a person's own fields, every alias they are known by, their
// biography (decoded back out of fields_json), the genres tagged anywhere in
// their discography (Person carries no genre of its own, ADR-32's own fields
// are biographical), and their discography -- every work credited to them,
// any role (architecture.md's own GET /people/{id} sketch: "incl. coverage
// per media type"). Coverage itself (held vs missing vs upgradable) reuses
// Satisfaction over desired_item, populated for music since todo 4.4 -- not
// wired into this endpoint yet, so this pass answers the discography half only.
type PersonDetail struct {
	Person      librarydb.Person
	Aliases     []string
	Biography   string
	Genres      []string
	Discography []DiscographyRow
}

// Detail composes one person's full detail. A missing id surfaces
// sql.ErrNoRows through GetPerson, wrapped: callers use errors.Is to detect it.
func (p People) Detail(ctx context.Context, id int64) (PersonDetail, error) {
	queries := librarydb.New(p.store.Read())

	person, err := queries.GetPerson(ctx, id)
	if err != nil {
		return PersonDetail{}, fmt.Errorf("library: person detail: get person: %w", err)
	}

	aliases, err := queries.ListAliasesForPerson(ctx, id)
	if err != nil {
		return PersonDetail{}, fmt.Errorf("library: person detail: aliases: %w", err)
	}

	genres, err := queries.ListGenresForPersonDiscography(ctx, id)
	if err != nil {
		return PersonDetail{}, fmt.Errorf("library: person detail: genres: %w", err)
	}

	discography, err := p.Discography(ctx, id, "")
	if err != nil {
		return PersonDetail{}, fmt.Errorf("library: person detail: discography: %w", err)
	}

	return PersonDetail{
		Person: person, Aliases: aliases, Biography: biographyFromFields(person.FieldsJson),
		Genres: genres, Discography: discography,
	}, nil
}

// SubscriptionRequest is what a caller supplies to subscribe to a person
// (ADR-32): media types x roles, optionally the whole back catalogue.
type SubscriptionRequest struct {
	MediaTypes           []string
	Roles                []string
	MonitorBackCatalogue bool
}

// Subscribe records a subscription and marks the person monitored, in one
// transaction -- ADR-32: "adding a performer monitors their scenes regardless
// of studio". CategoryID and ProfileID are deliberately left unset: both are
// acquisition-planner concerns (which category's profile decides what to
// grab), and there is no acquisition planner for music yet.
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
	mediaTypesJSON, err := json.Marshal(request.MediaTypes)
	if err != nil {
		return 0, fmt.Errorf("library: subscribe: encode media types: %w", err)
	}
	var rolesJSON sql.NullString
	if len(request.Roles) > 0 {
		encoded, err := json.Marshal(request.Roles)
		if err != nil {
			return 0, fmt.Errorf("library: subscribe: encode roles: %w", err)
		}
		rolesJSON = sql.NullString{String: string(encoded), Valid: true}
	}
	subscriptionUserID := sql.NullInt64{Int64: userID, Valid: userID != 0}

	var subscriptionID int64
	err = p.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		// subscription.subject_id is a polymorphic reference with no FK to
		// enforce it (person, organisation and tag all share the column) --
		// without this check, subscribing to a nonexistent person would
		// silently succeed rather than reporting sql.ErrNoRows.
		if _, err := queries.GetPerson(ctx, personID); err != nil {
			return err
		}
		if existing, err := queries.GetSubscriptionForSubject(ctx, librarydb.GetSubscriptionForSubjectParams{
			SubjectType: "person", SubjectID: personID, UserID: subscriptionUserID,
		}); err == nil {
			subscriptionID = existing.ID
			return nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("look up existing subscription: %w", err)
		}

		monitorBackCatalogue := int64(0)
		if request.MonitorBackCatalogue {
			monitorBackCatalogue = 1
		}
		subscription, err := queries.CreateSubscription(ctx, librarydb.CreateSubscriptionParams{
			SubjectType: "person", SubjectID: personID,
			MediaTypesJson: string(mediaTypesJSON), RolesJson: rolesJSON,
			MonitorBackCatalogue: monitorBackCatalogue, Enabled: 1,
			UserID: subscriptionUserID, CreatedAt: at.Unix(),
		})
		if err != nil {
			return fmt.Errorf("create subscription: %w", err)
		}
		subscriptionID = subscription.ID
		return queries.SetPersonMonitored(ctx, librarydb.SetPersonMonitoredParams{Monitored: 1, ID: personID})
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
}

// personFieldsJSON is empty when identity has nothing worth recording, so a
// person resolved with no biography gets a NULL fields_json rather than "{}".
func personFieldsJSON(identity PersonIdentity) string {
	if identity.Biography == "" {
		return ""
	}
	encoded, err := json.Marshal(personFields{Biography: identity.Biography})
	if err != nil {
		return ""
	}
	return string(encoded)
}
