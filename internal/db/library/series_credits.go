package library

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// seriesCreditChunk keeps each credit transaction under the bulk-write budget (ADR-3).
const seriesCreditChunk = 200

// writeCredits resolves each credited person by provider id, creating who is new, and stores the credit on the work
// or on the episode its PartRef names. A refresh adds and updates; a credit a provider dropped is left in place.
func (s Series) writeCredits(ctx context.Context, at time.Time, workID int64, partIDs map[string]int64, credits []SeriesCreditWrite) error {
	for start := 0; start < len(credits); start += seriesCreditChunk {
		chunk := credits[start:min(start+seriesCreditChunk, len(credits))]
		err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, credit := range chunk {
				partID := int64(0)
				if credit.PartRef != "" {
					id, found := partIDs[credit.PartRef]
					if !found {
						continue
					}
					partID = id
				}
				personID, err := findOrCreatePerson(ctx, queries, at, PersonLink{
					Provider: credit.Provider, ExternalID: credit.ExternalID,
					Person: PersonIdentity{Name: credit.Name, Kind: "Person", ImageURL: credit.ImageURL},
				})
				if err != nil {
					return fmt.Errorf("person %s:%s: %w", credit.Provider, credit.ExternalID, err)
				}
				if credit.ImageURL != "" {
					if err := queries.SetPersonImageURL(ctx, librarydb.SetPersonImageURLParams{ImageUrl: credit.ImageURL, ID: personID}); err != nil {
						return fmt.Errorf("person image: %w", err)
					}
				}
				if err := queries.AddCredit(ctx, librarydb.AddCreditParams{
					PersonID: personID, WorkID: workID, EditionID: 0, PartID: partID, Role: credit.Role,
					Character: credit.Character, Ord: sql.NullInt64{Int64: int64(credit.Ord), Valid: true},
				}); err != nil {
					return fmt.Errorf("credit: %w", err)
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: write series credits: %w", err)
		}
	}
	return nil
}
