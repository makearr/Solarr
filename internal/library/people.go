package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
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

// AddCredit records a work-level credit (edition_id/part_id zero, Appendix E
// D2) for an already-resolved person. ord is the credit order (0-based).
func (p People) AddCredit(ctx context.Context, workID, personID int64, role string, ord int) error {
	err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: personID, WorkID: workID, EditionID: 0, PartID: 0,
			Role: role, Character: "", Ord: sql.NullInt64{Int64: int64(ord), Valid: true},
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
