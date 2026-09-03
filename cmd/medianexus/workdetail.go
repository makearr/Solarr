package main

import (
	"context"
	"time"

	"github.com/makearr/MediaNexus/internal/apiserver"
	"github.com/makearr/MediaNexus/internal/library"
)

// workDetailAdapter satisfies apiserver.WorkDetailReader over library.Works.
// Neither package imports the other (worksAdapter's doc comment explains why),
// so something in between has to translate. External id rows are built inline
// rather than through a shared helper, so this file never has to name a
// librarydb type -- cmd/medianexus's allowed imports (internal/archtest) do
// not include internal/store/gen/librarydb.
type workDetailAdapter struct{ works library.Works }

func (a workDetailAdapter) GetWorkDetail(ctx context.Context, id int64) (apiserver.WorkDetailRow, error) {
	detail, err := a.works.Detail(ctx, id)
	if err != nil {
		return apiserver.WorkDetailRow{}, err
	}

	externalIDs := make([]apiserver.ExternalIDRow, len(detail.ExternalIDs))
	for index, external := range detail.ExternalIDs {
		externalIDs[index] = apiserver.ExternalIDRow{Provider: external.Provider, ExternalID: external.ExternalID}
	}

	row := apiserver.WorkDetailRow{
		ID: detail.Work.ID, MediaType: detail.Work.MediaType, Title: detail.Work.CanonicalTitle,
		SortTitle:     detail.Work.SortTitle,
		AddedAt:       time.Unix(detail.Work.AddedAt, 0),
		UpdatedAt:     time.Unix(detail.Work.UpdatedAt, 0),
		ExternalIDs:   externalIDs,
		Editions:      editionRows(detail.Editions),
		Parts:         partRows(detail.Parts),
		RootFolderIDs: detail.RootFolderIDs,
	}
	if detail.Work.Year.Valid {
		row.Year = &detail.Work.Year.Int64
	}
	if detail.Work.RuntimeMs.Valid {
		row.RuntimeMs = &detail.Work.RuntimeMs.Int64
	}
	if detail.Work.OriginalLanguage.Valid {
		row.Language = &detail.Work.OriginalLanguage.String
	}
	return row, nil
}

func editionRows(from []library.EditionDetail) []apiserver.EditionRow {
	rows := make([]apiserver.EditionRow, len(from))
	for index, detail := range from {
		externalIDs := make([]apiserver.ExternalIDRow, len(detail.ExternalIDs))
		for j, external := range detail.ExternalIDs {
			externalIDs[j] = apiserver.ExternalIDRow{Provider: external.Provider, ExternalID: external.ExternalID}
		}

		row := apiserver.EditionRow{
			ID: detail.Edition.ID, Kind: detail.Edition.Kind, Label: detail.Edition.Label,
			IsDefault:   detail.Edition.IsDefault != 0,
			ExternalIDs: externalIDs,
		}
		if detail.Edition.Year.Valid {
			row.Year = &detail.Edition.Year.Int64
		}
		if detail.Edition.Format.Valid {
			row.Format = &detail.Edition.Format.String
		}
		if detail.Edition.CatalogueNumber.Valid {
			row.CatalogueNumber = &detail.Edition.CatalogueNumber.String
		}
		if detail.Edition.Barcode.Valid {
			row.Barcode = &detail.Edition.Barcode.String
		}
		if detail.Edition.RuntimeMs.Valid {
			row.RuntimeMs = &detail.Edition.RuntimeMs.Int64
		}
		rows[index] = row
	}
	return rows
}

func partRows(from []library.PartDetail) []apiserver.PartRow {
	rows := make([]apiserver.PartRow, len(from))
	for index, detail := range from {
		externalIDs := make([]apiserver.ExternalIDRow, len(detail.ExternalIDs))
		for j, external := range detail.ExternalIDs {
			externalIDs[j] = apiserver.ExternalIDRow{Provider: external.Provider, ExternalID: external.ExternalID}
		}

		row := apiserver.PartRow{
			ID: detail.Part.ID, MediaRef: detail.Part.MediaRef, SortKey: detail.Part.SortKey,
			Monitored:   detail.Part.Monitored != 0,
			ExternalIDs: externalIDs,
		}
		if detail.Part.Title.Valid {
			row.Title = &detail.Part.Title.String
		}
		if detail.Part.RuntimeMs.Valid {
			row.RuntimeMs = &detail.Part.RuntimeMs.Int64
		}
		if detail.Part.Date.Valid {
			row.Date = &detail.Part.Date.String
		}
		rows[index] = row
	}
	return rows
}
