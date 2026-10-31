package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// Person statuses; a person with neither a stated status nor a death date counts as active.
const (
	PersonActive   = "active"
	PersonInactive = "inactive"
	PersonDeceased = "deceased"
)

// PersonStatuses are the values the status filter and the edit form offer.
var PersonStatuses = []string{PersonActive, PersonInactive, PersonDeceased}

// PersonAttributes are the descriptive facts of a person that a list filters on; empty or zero is unknown.
type PersonAttributes struct {
	Nationality  string `json:"nationality,omitempty"`
	HeightCm     int    `json:"height_cm,omitempty"`
	WeightKg     int    `json:"weight_kg,omitempty"`
	Measurements string `json:"measurements,omitempty"`
	HairColor    string `json:"hair_color,omitempty"`
	Ethnicity    string `json:"ethnicity,omitempty"`
	Status       string `json:"status,omitempty"`
	Tattoos      string `json:"tattoos,omitempty"`
	Piercings    string `json:"piercings,omitempty"`
}

// Over answers the attributes with every one the other states taken from it.
func (a PersonAttributes) Over(other PersonAttributes) PersonAttributes {
	pick := func(mine, theirs string) string {
		if theirs != "" {
			return theirs
		}
		return mine
	}
	a.Nationality, a.Measurements = pick(a.Nationality, other.Nationality), pick(a.Measurements, other.Measurements)
	a.HairColor, a.Ethnicity = pick(a.HairColor, other.HairColor), pick(a.Ethnicity, other.Ethnicity)
	a.Status, a.Tattoos, a.Piercings = pick(a.Status, other.Status), pick(a.Tattoos, other.Tattoos), pick(a.Piercings, other.Piercings)
	if other.HeightCm > 0 {
		a.HeightCm = other.HeightCm
	}
	if other.WeightKg > 0 {
		a.WeightKg = other.WeightKg
	}
	return a
}

// attributesFromFields reads the provider's attributes, and the ones a person set by hand over them; the status falls back
// to the death date.
func attributesFromFields(raw sql.NullString, died string) PersonAttributes {
	var fields struct {
		Provider PersonAttributes `json:"attributes"`
		ByHand   PersonAttributes `json:"attributes_hand"`
	}
	if raw.Valid {
		_ = json.Unmarshal([]byte(raw.String), &fields)
	}
	attributes := fields.Provider.Over(fields.ByHand)
	if attributes.Status == "" && died != "" {
		attributes.Status = PersonDeceased
	}
	return attributes
}

// setFieldsKey puts a JSON value under one key of the person's fields_json, leaving the rest.
func setFieldsKey(ctx context.Context, tx *sql.Tx, personID int64, key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE person SET fields_json = json_set(COALESCE(NULLIF(fields_json, ''), '{}'), '$.`+key+`', json(?)) WHERE id = ?`,
		string(encoded), personID)
	return err
}

// PersonEdit is what a person sets by hand on a person: the name, the gender and the attributes. Each stays through a
// provider's refresh.
type PersonEdit struct {
	Name       string
	Gender     string
	Attributes PersonAttributes
}

// ErrPersonName is an edit that leaves the person without a name.
var ErrPersonName = errors.New("library: a person needs a name")

// Edit sets the name, gender and attributes of a person by hand. A new name keeps the old one as an alias, and no
// provider's name replaces it afterwards.
func (p People) Edit(ctx context.Context, personID int64, edit PersonEdit) error {
	name := strings.TrimSpace(edit.Name)
	if name == "" {
		return ErrPersonName
	}
	err := p.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		current, err := queries.GetPerson(ctx, personID)
		if err != nil {
			return err
		}
		if current.Name != name {
			if err := queries.AddPersonAlias(ctx, librarydb.AddPersonAliasParams{
				PersonID: personID, Alias: current.Name, Normalized: normalizedAlias(current.Name), Source: "rename",
			}); err != nil {
				return err
			}
			if err := queries.RenamePersonByHand(ctx, librarydb.RenamePersonByHandParams{Name: name, ID: personID}); err != nil {
				return err
			}
		}
		if err := queries.SetPersonGender(ctx, librarydb.SetPersonGenderParams{Gender: nullString(strings.TrimSpace(edit.Gender)), ID: personID}); err != nil {
			return err
		}
		return setFieldsKey(ctx, tx, personID, "attributes_hand", edit.Attributes)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPersonNotFound
	}
	if err != nil {
		return fmt.Errorf("library: edit person %d: %w", personID, err)
	}
	return nil
}

// ErrMergeSelf is a merge of a person into themselves.
var ErrMergeSelf = errors.New("library: a person cannot be merged into themselves")

// MergePeople folds the loser into the winner: credits, ids, tags, pictures and subscriptions move, the loser's name
// becomes an alias of the winner, and the loser is deleted. Files stay where they are.
func (p People) MergePeople(ctx context.Context, loserID, winnerID int64) error {
	if loserID == winnerID {
		return ErrMergeSelf
	}
	err := p.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		loser, err := queries.GetPerson(ctx, loserID)
		if err != nil {
			return err
		}
		winner, err := queries.GetPerson(ctx, winnerID)
		if err != nil {
			return err
		}
		return mergeArtist(ctx, queries, ArtistMerge{LoserID: loserID, WinnerID: winnerID, LoserName: loser.Name, WinnerName: winner.Name})
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPersonNotFound
	}
	if err != nil {
		return fmt.Errorf("library: merge person %d into %d: %w", loserID, winnerID, err)
	}
	return nil
}
