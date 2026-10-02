package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// seriesPartChunk keeps each write transaction under the bulk-write budget (ADR-3).
const seriesPartChunk = 200

// ExternalIDWrite is one provider id of a work, with how sure the provider is of the mapping.
type ExternalIDWrite struct {
	Provider, ExternalID string
	Confidence           float64
}

// AltNumberWrite is a part's number under another scheme: scene, absolute, dvd, tmdb (ADR-41).
type AltNumberWrite struct{ Scheme, Provider, Ref string }

// SeriesPartWrite is one episode as the module sees it; MonitoredIfNew applies only to a part this call creates.
type SeriesPartWrite struct {
	MediaRef, SortKey, Title            string
	RuntimeMs                           *int64
	Date                                string
	AvailableAt                         *int64
	AvailableConfidence, AvailableBasis string
	MonitoredIfNew                      bool
	AltNumbers                          []AltNumberWrite
	ExternalIDs                         []ExternalIDWrite
}

// SeriesFieldWrite is one provider's value of a field, kept beside the others for the field-level merge (ADR-8).
type SeriesFieldWrite struct{ Field, Provider, Value string }

// SeriesDateWrite is a typed work date such as first_aired.
type SeriesDateWrite struct{ Kind, Date, Source string }

// SeriesRatingWrite is one provider's score, normalised to 0..10.
type SeriesRatingWrite struct {
	Provider string
	Score    float64
	Votes    int64
}

// SeriesWrite is everything a series refresh stores. Naming and persistence only: what the values mean is the
// TV module's business (ADR-23).
type SeriesWrite struct {
	Work             WorkIdentity
	Year             *int64
	OriginalLanguage string
	RuntimeMs        *int64
	Description      string
	// IDs lists every provider id; the first one already in the library decides which work this is.
	IDs            []ExternalIDWrite
	Aliases        []string
	AliasSource    string
	Fields         []SeriesFieldWrite
	Genres         []string
	GenreSource    string
	Network        string
	NetworkCountry string
	Dates          []SeriesDateWrite
	Rating         *SeriesRatingWrite
	Certification  string
	Parts          []SeriesPartWrite
}

// SeriesResult reports what a refresh did.
type SeriesResult struct {
	WorkID       int64
	Created      bool
	PartsCreated int
	PartsUpdated int
	// PartIDs maps media_ref to work_part.id for every part written.
	PartIDs map[string]int64
	// IDsSkipped counts provider ids another work already owns; they are left with their owner, never stolen.
	IDsSkipped int
}

// Series writes TV works, their episodes and provider metadata.
type Series struct{ store *store.Store }

// NewSeries builds it over an open library store.
func NewSeries(opened *store.Store) Series { return Series{store: opened} }

// Upsert finds the work any of the series' ids already names, or creates one, then writes its metadata and parts. A
// refresh never changes monitoring, never removes a part and never overwrites a manual field.
func (s Series) Upsert(ctx context.Context, at time.Time, write SeriesWrite) (SeriesResult, error) {
	var result SeriesResult
	err := s.store.Write(ctx, store.PrioNormal, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		workID, created, skipped, err := upsertSeriesWork(ctx, queries, at, write)
		if err != nil {
			return err
		}
		result = SeriesResult{WorkID: workID, Created: created, IDsSkipped: skipped, PartIDs: map[string]int64{}}
		return writeSeriesMetadata(ctx, queries, at, workID, write)
	})
	if err != nil {
		return SeriesResult{}, err
	}

	existing, err := s.existingPartRefs(ctx, result.WorkID)
	if err != nil {
		return SeriesResult{}, err
	}
	for start := 0; start < len(write.Parts); start += seriesPartChunk {
		chunk := write.Parts[start:min(start+seriesPartChunk, len(write.Parts))]
		err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, part := range chunk {
				id, err := upsertSeriesPart(ctx, queries, result.WorkID, part)
				if err != nil {
					return err
				}
				result.PartIDs[part.MediaRef] = id
			}
			return nil
		})
		if err != nil {
			return SeriesResult{}, fmt.Errorf("library: write series parts: %w", err)
		}
	}
	for _, part := range write.Parts {
		if existing[part.MediaRef] {
			result.PartsUpdated++
		} else {
			result.PartsCreated++
		}
	}
	return result, nil
}

func (s Series) existingPartRefs(ctx context.Context, workID int64) (map[string]bool, error) {
	rows, err := librarydb.New(s.store.Read()).ListWorkPartRefs(ctx, workID)
	if err != nil {
		return nil, fmt.Errorf("library: list parts: %w", err)
	}
	refs := make(map[string]bool, len(rows))
	for _, row := range rows {
		refs[row.MediaRef] = true
	}
	return refs, nil
}

func upsertSeriesWork(ctx context.Context, queries *librarydb.Queries, at time.Time, write SeriesWrite) (workID int64, created bool, skipped int, err error) {
	for _, id := range write.IDs {
		found, err := queries.GetWorkByExternalID(ctx, librarydb.GetWorkByExternalIDParams{Provider: id.Provider, ExternalID: id.ExternalID})
		if err == nil {
			workID = found.ID
			break
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, false, 0, fmt.Errorf("library: look up work %s:%s: %w", id.Provider, id.ExternalID, err)
		}
	}
	if workID == 0 {
		row, err := queries.CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType: write.Work.MediaType, CanonicalTitle: write.Work.CanonicalTitle, SortTitle: write.Work.SortTitle,
			OriginalLanguage: nullString(write.OriginalLanguage), Year: nullInt64(write.Year), RuntimeMs: nullInt64(write.RuntimeMs),
			AddedAt: at.Unix(), UpdatedAt: at.Unix(),
		})
		if err != nil {
			return 0, false, 0, fmt.Errorf("library: create series work: %w", err)
		}
		workID, created = row.ID, true
	}

	for _, id := range write.IDs {
		owner, err := queries.GetWorkByExternalID(ctx, librarydb.GetWorkByExternalIDParams{Provider: id.Provider, ExternalID: id.ExternalID})
		switch {
		case err == nil && owner.ID != workID:
			skipped++
			continue
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return 0, false, 0, fmt.Errorf("library: look up work %s:%s: %w", id.Provider, id.ExternalID, err)
		}
		confidence := id.Confidence
		if confidence == 0 {
			confidence = 1
		}
		if err := queries.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: id.Provider, ExternalID: id.ExternalID, WorkID: workID, Confidence: confidence, FirstSeenAt: at.Unix(),
		}); err != nil {
			return 0, false, 0, fmt.Errorf("library: link work id: %w", err)
		}
	}
	return workID, created, skipped, nil
}

func writeSeriesMetadata(ctx context.Context, queries *librarydb.Queries, at time.Time, workID int64, write SeriesWrite) error {
	manualTitle, err := queries.CountWorkFieldManual(ctx, librarydb.CountWorkFieldManualParams{WorkID: workID, Field: "title"})
	if err != nil {
		return fmt.Errorf("library: read manual title: %w", err)
	}
	if manualTitle == 0 && write.Work.CanonicalTitle != "" {
		if err := queries.SetWorkTitle(ctx, librarydb.SetWorkTitleParams{
			CanonicalTitle: write.Work.CanonicalTitle, SortTitle: write.Work.SortTitle, UpdatedAt: at.Unix(), ID: workID,
		}); err != nil {
			return fmt.Errorf("library: set series title: %w", err)
		}
	}
	if err := queries.UpdateWorkSeriesDetails(ctx, librarydb.UpdateWorkSeriesDetailsParams{
		OriginalLanguage: nullString(write.OriginalLanguage), Year: nullInt64(write.Year), RuntimeMs: nullInt64(write.RuntimeMs),
		Description: nullString(write.Description), UpdatedAt: at.Unix(), ID: workID,
	}); err != nil {
		return fmt.Errorf("library: update series details: %w", err)
	}

	source := write.AliasSource
	if err := queries.DeleteWorkTitlesBySource(ctx, librarydb.DeleteWorkTitlesBySourceParams{WorkID: workID, Source: nullString(source)}); err != nil {
		return fmt.Errorf("library: clear aliases: %w", err)
	}
	for _, alias := range write.Aliases {
		if strings.TrimSpace(alias) == "" {
			continue
		}
		if _, err := queries.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
			WorkID: workID, Title: alias, Normalized: strings.ToLower(strings.Join(strings.Fields(alias), " ")), Kind: "alias", Source: nullString(source),
		}); err != nil {
			return fmt.Errorf("library: add alias: %w", err)
		}
	}

	for _, field := range write.Fields {
		if err := queries.SetWorkField(ctx, librarydb.SetWorkFieldParams{
			WorkID: workID, Field: field.Field, Provider: field.Provider, Value: nullString(field.Value), FetchedAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("library: set field %s: %w", field.Field, err)
		}
	}
	for _, date := range write.Dates {
		if err := queries.SetWorkDate(ctx, librarydb.SetWorkDateParams{WorkID: workID, Kind: date.Kind, Date: date.Date, Source: date.Source}); err != nil {
			return fmt.Errorf("library: set date: %w", err)
		}
	}
	if len(write.Genres) > 0 {
		if err := queries.ClearWorkClassification(ctx, librarydb.ClearWorkClassificationParams{WorkID: workID, Kind: "genre"}); err != nil {
			return fmt.Errorf("library: clear genres: %w", err)
		}
		for _, genre := range write.Genres {
			if err := queries.SetWorkClassification(ctx, librarydb.SetWorkClassificationParams{WorkID: workID, Kind: "genre", Value: genre, Source: write.GenreSource}); err != nil {
				return fmt.Errorf("library: set genre: %w", err)
			}
		}
	}
	if write.Rating != nil {
		if err := queries.SetWorkRating(ctx, librarydb.SetWorkRatingParams{
			WorkID: workID, Provider: write.Rating.Provider, Score: write.Rating.Score, Votes: sql.NullInt64{Int64: write.Rating.Votes, Valid: write.Rating.Votes > 0}, FetchedAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("library: set rating: %w", err)
		}
	}
	if write.Certification != "" {
		if err := queries.SetWorkCertification(ctx, librarydb.SetWorkCertificationParams{WorkID: workID, System: "tv", Rating: write.Certification}); err != nil {
			return fmt.Errorf("library: set certification: %w", err)
		}
	}
	return writeSeriesNetwork(ctx, queries, workID, write)
}

func writeSeriesNetwork(ctx context.Context, queries *librarydb.Queries, workID int64, write SeriesWrite) error {
	if write.Network == "" {
		return nil
	}
	organisation, err := queries.FindOrganisationByName(ctx, librarydb.FindOrganisationByNameParams{Kind: "network", Name: write.Network})
	if errors.Is(err, sql.ErrNoRows) {
		organisation, err = queries.CreateOrganisation(ctx, librarydb.CreateOrganisationParams{
			Name: write.Network, SortName: strings.ToLower(write.Network), Kind: "network", Country: nullString(write.NetworkCountry),
		})
	}
	if err != nil {
		return fmt.Errorf("library: network %q: %w", write.Network, err)
	}
	if err := queries.ClearWorkOrganisationRole(ctx, librarydb.ClearWorkOrganisationRoleParams{WorkID: workID, Role: "network"}); err != nil {
		return fmt.Errorf("library: clear network: %w", err)
	}
	return queries.LinkWorkOrganisation(ctx, librarydb.LinkWorkOrganisationParams{WorkID: workID, OrganisationID: organisation.ID, Role: "network"})
}

func upsertSeriesPart(ctx context.Context, queries *librarydb.Queries, workID int64, part SeriesPartWrite) (int64, error) {
	row, err := queries.UpsertWorkPart(ctx, librarydb.UpsertWorkPartParams{
		WorkID: workID, MediaRef: part.MediaRef, SortKey: part.SortKey, Title: nullString(part.Title), RuntimeMs: nullInt64(part.RuntimeMs),
		Date: nullString(part.Date), Monitored: boolInt(part.MonitoredIfNew), AvailableAt: nullInt64(part.AvailableAt),
		AvailableConfidence: orDefault(part.AvailableConfidence, "unknown"), AvailableBasis: part.AvailableBasis,
	})
	if err != nil {
		return 0, fmt.Errorf("library: upsert part %s: %w", part.MediaRef, err)
	}
	for _, alt := range part.AltNumbers {
		if err := queries.AddAltNumbering(ctx, librarydb.AddAltNumberingParams{PartID: row.ID, Scheme: alt.Scheme, Provider: alt.Provider, AltRef: alt.Ref}); err != nil {
			return 0, fmt.Errorf("library: alt number %s: %w", part.MediaRef, err)
		}
	}
	for _, id := range part.ExternalIDs {
		confidence := id.Confidence
		if confidence == 0 {
			confidence = 1
		}
		if err := queries.LinkWorkPartExternalID(ctx, librarydb.LinkWorkPartExternalIDParams{Provider: id.Provider, ExternalID: id.ExternalID, PartID: row.ID, Confidence: confidence}); err != nil {
			return 0, fmt.Errorf("library: part id %s: %w", part.MediaRef, err)
		}
	}
	return row.ID, nil
}

func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// WorkByProviderID answers the series a provider id names, if the library has one.
func (s Series) WorkByProviderID(ctx context.Context, provider, externalID string) (int64, bool, error) {
	work, err := librarydb.New(s.store.Read()).GetWorkByExternalID(ctx, librarydb.GetWorkByExternalIDParams{Provider: provider, ExternalID: externalID})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("library: look up work %s:%s: %w", provider, externalID, err)
	}
	return work.ID, true, nil
}
