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

// seriesPartChunk keeps each write transaction under the bulk-write budget (ADR-3); a part is several statements.
const seriesPartChunk = 60

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
	// AvailableGrace is the wait after its time or date, stamped from the settings; nil takes its confidence's default.
	AvailableGrace *time.Duration
	MonitoredIfNew bool
	AltNumbers     []AltNumberWrite
	ExternalIDs    []ExternalIDWrite
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

// SeriesCreditWrite is one person's credit on the series; PartRef scopes it to one episode, empty for the whole run.
type SeriesCreditWrite struct {
	Provider, ExternalID, Name, ImageURL string
	Role, Character                      string
	Ord                                  int
	PartRef                              string
}

// SeriesNetworkWrite is a further network or streamer the series airs on.
type SeriesNetworkWrite struct {
	Name, Country, Provider, ExternalID, LogoURL string
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
	IDs         []ExternalIDWrite
	Aliases     []string
	AliasSource string
	Fields      []SeriesFieldWrite
	Genres      []string
	GenreSource string
	// GenreTaxonomy is the tag tree the genres are mapped onto, the media type's.
	GenreTaxonomy  string
	Network        string
	NetworkCountry string
	// OrganisationKind is what Network and MoreNetworks are: a TV "network" (the default), a movie "studio".
	OrganisationKind string
	Dates            []SeriesDateWrite
	Rating           *SeriesRatingWrite
	// MoreRatings are further providers' scores of the work, stored beside Rating.
	MoreRatings   []SeriesRatingWrite
	Certification string
	// CertificationSystem names the scheme Certification is in: "tv" (the default) or "movie".
	CertificationSystem string
	// CertificationAge is the youngest age Certification admits, -1 when the type cannot read it.
	CertificationAge int64
	// MoreNetworks are linked beside Network, which stays the primary one; one that names it adds its id and logo.
	MoreNetworks []SeriesNetworkWrite
	Keywords     []string
	Credits      []SeriesCreditWrite
	Parts        []SeriesPartWrite
	// LeadRole is the credit role the work is listed under (a movie's director); empty lists it under no one.
	LeadRole string
	// Franchise is the collection a provider says the work belongs to (a movie's franchise); nil leaves it as it is.
	Franchise *SeriesFranchiseWrite
}

// SeriesFranchiseWrite names a franchise by the provider's id for it.
type SeriesFranchiseWrite struct{ Provider, ExternalID, Name, PosterURL string }

func (w SeriesWrite) organisationKind() string { return orDefault(w.OrganisationKind, "network") }

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
	if err := s.writeCredits(ctx, at, result.WorkID, result.PartIDs, write.Credits); err != nil {
		return SeriesResult{}, err
	}
	if write.LeadRole != "" {
		err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			return librarydb.New(tx).SetWorkLeadFromRole(ctx, librarydb.SetWorkLeadFromRoleParams{WorkID: result.WorkID, Role: write.LeadRole})
		})
		if err != nil {
			return SeriesResult{}, fmt.Errorf("library: set lead of work %d: %w", result.WorkID, err)
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
		found, err := queries.GetWorkOfTypeByExternalID(ctx, librarydb.GetWorkOfTypeByExternalIDParams{
			Provider: id.Provider, ExternalID: id.ExternalID, MediaType: write.Work.MediaType,
		})
		if err == nil {
			conflict, err := holdsOtherID(ctx, queries, found.ID, write.IDs[0])
			if err != nil {
				return 0, false, 0, err
			}
			if conflict {
				continue
			}
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

// holdsOtherID is a work that already carries ids of the anchor's provider, none of them the anchor: a second id of another
// provider must not fold two series into one (a wrong cross-reference once joined a show to its sequel).
func holdsOtherID(ctx context.Context, queries *librarydb.Queries, workID int64, anchor ExternalIDWrite) (bool, error) {
	held, err := queries.ListWorkExternalIDs(ctx, workID)
	if err != nil {
		return false, fmt.Errorf("library: read work ids: %w", err)
	}
	other := false
	for _, id := range held {
		if id.Provider != anchor.Provider {
			continue
		}
		if id.ExternalID == anchor.ExternalID {
			return false, nil
		}
		other = true
	}
	return other, nil
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
	if len(write.Genres) > 0 && write.GenreTaxonomy != "" {
		if err := assignGenres(ctx, queries, workID, write.GenreTaxonomy, write.GenreSource, write.Genres); err != nil {
			return fmt.Errorf("library: genres: %w", err)
		}
	}
	if write.Rating != nil {
		if err := queries.SetWorkRating(ctx, librarydb.SetWorkRatingParams{
			WorkID: workID, Provider: write.Rating.Provider, Score: write.Rating.Score, Votes: sql.NullInt64{Int64: write.Rating.Votes, Valid: write.Rating.Votes > 0}, FetchedAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("library: set rating: %w", err)
		}
	}
	for _, rating := range write.MoreRatings {
		if err := queries.SetWorkRating(ctx, librarydb.SetWorkRatingParams{
			WorkID: workID, Provider: rating.Provider, Score: rating.Score, Votes: sql.NullInt64{Int64: rating.Votes, Valid: rating.Votes > 0}, FetchedAt: at.Unix(),
		}); err != nil {
			return fmt.Errorf("library: set rating: %w", err)
		}
	}
	if write.Certification != "" {
		system := orDefault(write.CertificationSystem, "tv")
		before, err := queries.GetCertificationAge(ctx, librarydb.GetCertificationAgeParams{WorkID: workID, System: system})
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("library: read certification: %w", err)
		}
		if before.Int64 != write.CertificationAge || !before.Valid {
			if err := queries.MarkWorkItemsDirty(ctx, workID); err != nil {
				return fmt.Errorf("library: plan again after a new rating: %w", err)
			}
		}
		if err := queries.SetWorkCertification(ctx, librarydb.SetWorkCertificationParams{
			WorkID: workID, System: system, Rating: write.Certification,
			Age: sql.NullInt64{Int64: write.CertificationAge, Valid: true},
		}); err != nil {
			return fmt.Errorf("library: set certification: %w", err)
		}
	}
	if err := writeSeriesKeywords(ctx, queries, workID, write); err != nil {
		return err
	}
	if write.Franchise != nil {
		var collectionID int64
		err := joinFranchiseTx(ctx, queries, &collectionID, at.Unix(), write.Work.MediaType, write.Franchise.Provider, write.Franchise.ExternalID, write.Franchise.Name, workID)
		if err == nil && write.Franchise.PosterURL != "" {
			err = queries.SetCollectionPoster(ctx, librarydb.SetCollectionPosterParams{PosterUrl: write.Franchise.PosterURL, ID: collectionID})
		}
		if err != nil {
			return fmt.Errorf("library: franchise: %w", err)
		}
	}
	return writeSeriesNetwork(ctx, queries, workID, write)
}

func writeSeriesKeywords(ctx context.Context, queries *librarydb.Queries, workID int64, write SeriesWrite) error {
	for _, keyword := range write.Keywords {
		if err := queries.SetWorkClassification(ctx, librarydb.SetWorkClassificationParams{WorkID: workID, Kind: "keyword", Value: keyword, Source: write.GenreSource}); err != nil {
			return fmt.Errorf("library: set keyword: %w", err)
		}
	}
	return nil
}

func writeSeriesNetwork(ctx context.Context, queries *librarydb.Queries, workID int64, write SeriesWrite) error {
	if write.Network == "" {
		return nil
	}
	before, err := queries.ListOrganisationsOfWork(ctx, workID)
	if err != nil {
		return fmt.Errorf("library: networks before: %w", err)
	}
	if err := writeSeriesPrimaryNetwork(ctx, queries, workID, write); err != nil {
		return err
	}
	for _, network := range write.MoreNetworks {
		if err := linkSeriesNetwork(ctx, queries, workID, network, write.organisationKind()); err != nil {
			return err
		}
	}
	after, err := queries.ListOrganisationsOfWork(ctx, workID)
	if err != nil {
		return fmt.Errorf("library: networks after: %w", err)
	}
	return refreshOrganisations(ctx, queries, append(before, after...))
}

// linkSeriesNetwork finds the network by its provider id, else by name, creates it when new, and links it to the work.
func linkSeriesNetwork(ctx context.Context, queries *librarydb.Queries, workID int64, network SeriesNetworkWrite, kind string) error {
	var id int64
	found, err := queries.GetOrganisationByExternalID(ctx, librarydb.GetOrganisationByExternalIDParams{Provider: network.Provider, ExternalID: network.ExternalID})
	if err == nil {
		id = found.ID
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("library: network %q: %w", network.Name, err)
	}
	if id == 0 {
		byName, err := queries.FindOrganisationByName(ctx, librarydb.FindOrganisationByNameParams{Kind: kind, Name: network.Name})
		switch {
		case err == nil:
			id = byName.ID
		case errors.Is(err, sql.ErrNoRows):
			created, err := queries.CreateOrganisation(ctx, librarydb.CreateOrganisationParams{
				Name: network.Name, SortName: strings.ToLower(network.Name), Kind: kind, Country: nullString(network.Country),
			})
			if err != nil {
				return fmt.Errorf("library: create network %q: %w", network.Name, err)
			}
			id = created.ID
		default:
			return fmt.Errorf("library: network %q: %w", network.Name, err)
		}
	}
	if network.ExternalID != "" {
		if err := queries.LinkOrganisationExternalID(ctx, librarydb.LinkOrganisationExternalIDParams{Provider: network.Provider, ExternalID: network.ExternalID, OrganisationID: id}); err != nil {
			return fmt.Errorf("library: network id %q: %w", network.Name, err)
		}
	}
	if network.LogoURL != "" {
		if err := queries.SetOrganisationLogo(ctx, librarydb.SetOrganisationLogoParams{Logo: network.LogoURL, ID: id}); err != nil {
			return fmt.Errorf("library: network logo %q: %w", network.Name, err)
		}
	}
	return queries.AddWorkOrganisation(ctx, librarydb.AddWorkOrganisationParams{WorkID: workID, OrganisationID: id, Role: kind})
}

func writeSeriesPrimaryNetwork(ctx context.Context, queries *librarydb.Queries, workID int64, write SeriesWrite) error {
	kind := write.organisationKind()
	organisation, err := queries.FindOrganisationByName(ctx, librarydb.FindOrganisationByNameParams{Kind: kind, Name: write.Network})
	if errors.Is(err, sql.ErrNoRows) {
		organisation, err = queries.CreateOrganisation(ctx, librarydb.CreateOrganisationParams{
			Name: write.Network, SortName: strings.ToLower(write.Network), Kind: kind, Country: nullString(write.NetworkCountry),
		})
	}
	if err != nil {
		return fmt.Errorf("library: network %q: %w", write.Network, err)
	}
	if err := queries.ClearWorkOrganisationRole(ctx, librarydb.ClearWorkOrganisationRoleParams{WorkID: workID, Role: kind}); err != nil {
		return fmt.Errorf("library: clear network: %w", err)
	}
	return queries.LinkWorkOrganisation(ctx, librarydb.LinkWorkOrganisationParams{WorkID: workID, OrganisationID: organisation.ID, Role: kind})
}

func upsertSeriesPart(ctx context.Context, queries *librarydb.Queries, workID int64, part SeriesPartWrite) (int64, error) {
	row, err := queries.UpsertWorkPart(ctx, librarydb.UpsertWorkPartParams{
		WorkID: workID, MediaRef: part.MediaRef, SortKey: part.SortKey, Title: nullString(part.Title), RuntimeMs: nullInt64(part.RuntimeMs),
		Date: nullString(part.Date), Monitored: boolInt(part.MonitoredIfNew), AvailableAt: nullInt64(part.AvailableAt),
		AvailableConfidence: orDefault(part.AvailableConfidence, "unknown"), AvailableBasis: part.AvailableBasis,
		AvailableGrace: graceSeconds(part.AvailableGrace),
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

// WorkByProviderID answers the work of the media type a provider id names, if the library has one.
func (s Series) WorkByProviderID(ctx context.Context, mediaType, provider, externalID string) (int64, bool, error) {
	work, err := librarydb.New(s.store.Read()).GetWorkOfTypeByExternalID(ctx, librarydb.GetWorkOfTypeByExternalIDParams{
		Provider: provider, ExternalID: externalID, MediaType: mediaType,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("library: look up work %s:%s: %w", provider, externalID, err)
	}
	return work.ID, true, nil
}

func graceSeconds(grace *time.Duration) sql.NullInt64 {
	if grace == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*grace / time.Second), Valid: true}
}

// StampGrace gives every part with a time or a date the wait grace says for its confidence, a batch at a time, and
// answers how many parts changed.
func (s Series) StampGrace(ctx context.Context, grace AvailabilityGrace) (int, error) {
	total := 0
	for {
		var changed int64
		err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			changed, err = librarydb.New(tx).StampPartGrace(ctx, librarydb.StampPartGraceParams{
				High: int64(grace.High / time.Second), Medium: int64(grace.Medium / time.Second),
				Low: int64(grace.Low / time.Second), Limit: dirtyBatchSize,
			})
			return err
		})
		if err != nil {
			return total, fmt.Errorf("library: stamp part grace: %w", err)
		}
		if changed == 0 {
			return total, nil
		}
		total += int(changed)
	}
}

// PartID is one part of a work with one provider's id of it.
type PartID struct {
	PartID     int64
	MediaRef   string
	Title      string
	ExternalID string
	HasFile    bool
}

// PartIDs answers every part of the work with the provider's id of each, "" where it has none.
func (s Series) PartIDs(ctx context.Context, workID int64, provider string) ([]PartID, error) {
	rows, err := librarydb.New(s.store.Read()).ListPartIDs(ctx, librarydb.ListPartIDsParams{Provider: provider, WorkID: workID})
	if err != nil {
		return nil, fmt.Errorf("library: part ids of work %d: %w", workID, err)
	}
	parts := make([]PartID, len(rows))
	for i, row := range rows {
		parts[i] = PartID{PartID: row.ID, MediaRef: row.MediaRef, Title: row.Title, ExternalID: row.ExternalID, HasFile: row.HasFile}
	}
	return parts, nil
}

// PartMove is one part taking a new place in its work's numbering.
type PartMove struct {
	PartID         int64
	OldRef, NewRef string
	NewSortKey     string
}

// UnplacedRef prefixes the place of a part a renumbering sets aside: out of every group, its files kept.
const UnplacedRef = "unplaced:"

// Renumber gives parts new places in one transaction, keeping each part's id, files, credits and history.
func (s Series) Renumber(ctx context.Context, workID int64, moves []PartMove) error {
	err := s.store.Write(ctx, store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		// Through a temporary name first, so two parts trading places never meet on one.
		for _, move := range moves {
			temporary := "renumber:" + move.NewRef
			if err := queries.SetPartRef(ctx, librarydb.SetPartRefParams{NewRef: temporary, SortKey: move.NewSortKey, PartID: move.PartID}); err != nil {
				return fmt.Errorf("part %d: %w", move.PartID, err)
			}
			if err := queries.SetItemFileRefs(ctx, librarydb.SetItemFileRefsParams{NewRef: temporary, OldRef: move.OldRef, WorkID: workID}); err != nil {
				return fmt.Errorf("files of part %d: %w", move.PartID, err)
			}
		}
		for _, move := range moves {
			if err := queries.SetPartRef(ctx, librarydb.SetPartRefParams{NewRef: move.NewRef, SortKey: move.NewSortKey, PartID: move.PartID}); err != nil {
				return fmt.Errorf("part %d: %w", move.PartID, err)
			}
			if err := queries.SetItemFileRefs(ctx, librarydb.SetItemFileRefsParams{NewRef: move.NewRef, OldRef: "renumber:" + move.NewRef, WorkID: workID}); err != nil {
				return fmt.Errorf("files of part %d: %w", move.PartID, err)
			}
		}
		return recomputeWorkItemsTx(ctx, queries, workID, s.store.Now())
	})
	if err != nil {
		return fmt.Errorf("library: renumber work %d: %w", workID, err)
	}
	return nil
}

// ErrPruneTooLarge is a prune that would remove more than the share of a series' parts it may.
var ErrPruneTooLarge = errors.New("library: prune would remove too many parts")

// PruneParts removes the parts of a series that a provider no longer lists (not in keep), but only those nothing holds
// on to: no file, no crossover event, no shared broadcast. More than maxShare of the series' parts is refused, since a
// provider that answered badly would otherwise empty a series. It answers the refs removed, and recounts the series.
func (s Series) PruneParts(ctx context.Context, workID int64, keep map[string]bool, maxShare float64) ([]string, error) {
	var removed []string
	err := s.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		all, err := queries.ListWorkPartRefs(ctx, workID)
		if err != nil {
			return err
		}
		candidates, err := queries.ListPrunableWorkParts(ctx, workID)
		if err != nil {
			return err
		}
		var drop []librarydb.ListPrunableWorkPartsRow
		for _, part := range candidates {
			if !keep[part.MediaRef] {
				drop = append(drop, part)
			}
		}
		if len(drop) == 0 {
			return nil
		}
		if float64(len(drop)) > maxShare*float64(len(all)) {
			return fmt.Errorf("%w: %d of %d", ErrPruneTooLarge, len(drop), len(all))
		}
		for _, part := range drop {
			if err := queries.DeleteWorkPart(ctx, part.ID); err != nil {
				return err
			}
			removed = append(removed, part.MediaRef)
		}
		return recomputeWorkItemsTx(ctx, queries, workID, s.store.Now())
	})
	if err != nil {
		return nil, err
	}
	return removed, nil
}
