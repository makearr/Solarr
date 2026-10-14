package adapters

import (
	"context"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// WorkDetail satisfies apiserver.WorkDetailReader over library.Works.
type WorkDetail struct {
	Works library.Works
	// Facets answers a media type's facets; nil shows none.
	Facets func(mediaType string) []library.WorkFacet
	// GroupLabel names a group of a media type's work; nil names it by its ref.
	GroupLabel func(mediaType, ref string) string
	// SearchStatus answers an album's last and next automatic search and why (F2); optional.
	SearchStatus func(ctx context.Context, itemID int64) (apiserver.WantedRow, error)
	// PartPosition places a part of a media type's work; nil numbers it by its ref.
	PartPosition func(mediaType, ref string) (section, number string)
}

func (a WorkDetail) GetWorkDetail(ctx context.Context, id int64) (apiserver.WorkDetailRow, error) {
	detail, err := a.Works.Detail(ctx, id)
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
		OnDisk:        detail.OnDisk(),
		AddedAt:       time.Unix(detail.Work.AddedAt, 0),
		UpdatedAt:     time.Unix(detail.Work.UpdatedAt, 0),
		ExternalIDs:   externalIDs,
		Editions:      editionRows(detail.Editions),
		PartCount:     int64(len(detail.Parts)),
		Credits:       creditRows(detail.Credits),
		Genres:        detail.Genres,
		RootFolderIDs: detail.RootFolderIDs,
		CategoryIDs:   detail.CategoryIDs,
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
	if detail.Work.Description.Valid {
		row.Description = &detail.Work.Description.String
	}
	if detail.Work.LeadPersonID.Valid {
		row.LeadID = detail.Work.LeadPersonID.Int64
	}
	if row.Pictures, err = a.Works.Pictures(ctx, id); err != nil {
		return apiserver.WorkDetailRow{}, err
	}
	if a.Facets != nil {
		values, err := a.Works.FacetValues(ctx, a.Facets(detail.Work.MediaType), []int64{id})
		if err != nil {
			return apiserver.WorkDetailRow{}, err
		}
		row.Facets = values[id]
	}
	for _, group := range detail.Groups {
		label := group.Ref
		if a.GroupLabel != nil {
			label = a.GroupLabel(detail.Work.MediaType, group.Ref)
		}
		row.Groups = append(row.Groups, apiserver.GroupRow{Ref: group.Ref, Label: label, Wanted: apiserver.WantedRow{
			ID: group.ID, Monitored: group.Monitored, RequiredCount: group.RequiredCount, PresentCount: group.PresentCount,
			State: group.State, LastSearchedAt: group.LastSearchedAt, AvailableFrom: group.AvailableFrom,
		}})
	}
	if detail.Wanted != nil {
		row.Wanted = &apiserver.WantedRow{
			ID: detail.Wanted.ID, Monitored: detail.Wanted.Monitored,
			RequiredCount: detail.Wanted.RequiredCount, PresentCount: detail.Wanted.PresentCount,
			State: detail.Wanted.State,
		}
		if a.SearchStatus != nil {
			status, err := a.SearchStatus(ctx, detail.Wanted.ID)
			if err != nil {
				return apiserver.WorkDetailRow{}, err
			}
			row.Wanted.LastSearchedAt, row.Wanted.NextSearchAt = status.LastSearchedAt, status.NextSearchAt
			row.Wanted.AutomaticSearch = status.AutomaticSearch
		}
	}
	return row, nil
}

// WorkPlaces answers the root folders and libraries a work is in.
func (a WorkDetail) WorkPlaces(ctx context.Context, id int64) ([]int64, []int64, error) {
	return a.Works.Places(ctx, id)
}

// ListWorkParts maps one page of a work's parts.
func (a WorkDetail) ListWorkParts(ctx context.Context, id int64, group string, after paginate.TextCursor, limit int) ([]apiserver.PartRow, error) {
	mediaType, err := a.Works.MediaTypeOf(ctx, id)
	if err != nil {
		return nil, err
	}
	parts, err := a.Works.Parts(ctx, id, group, library.PartCursor{SortKey: after.Sort, ID: after.ID}, limit)
	if err != nil {
		return nil, err
	}
	return a.partRows(mediaType, parts), nil
}

func creditRows(from []library.Credit) []apiserver.CreditRow {
	rows := make([]apiserver.CreditRow, len(from))
	for index, credit := range from {
		rows[index] = apiserver.CreditRow{
			PersonID: credit.PersonID, PersonName: credit.PersonName, Role: credit.Role,
			Character: credit.Character, ImageURL: credit.ImageURL,
		}
	}
	return rows
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

func (a WorkDetail) partRows(mediaType string, from []library.PartDetail) []apiserver.PartRow {
	rows := make([]apiserver.PartRow, len(from))
	for index, detail := range from {
		externalIDs := make([]apiserver.ExternalIDRow, len(detail.ExternalIDs))
		for j, external := range detail.ExternalIDs {
			externalIDs[j] = apiserver.ExternalIDRow{Provider: external.Provider, ExternalID: external.ExternalID}
		}

		row := apiserver.PartRow{
			ID: detail.Part.ID, MediaRef: detail.Part.MediaRef, SortKey: detail.Part.SortKey,
			Monitored:   detail.Part.Monitored != 0,
			ExternalIDs: externalIDs, AltNumbers: detail.AltNumbers, Number: detail.Part.MediaRef,
		}
		if a.PartPosition != nil {
			row.Section, row.Number = a.PartPosition(mediaType, detail.Part.MediaRef)
		}
		if detail.Part.AvailableAt.Valid {
			row.AvailableAt = time.Unix(detail.Part.AvailableAt.Int64, 0)
		}
		row.AvailableBasis, row.AvailableConfidence = detail.Part.AvailableBasis, detail.Part.AvailableConfidence
		for _, credit := range detail.Credits {
			row.Credits = append(row.Credits, apiserver.CreditRow{
				PersonID: credit.PersonID, PersonName: credit.PersonName, Role: credit.Role, Character: credit.Character,
			})
		}
		if detail.Wanted != nil {
			row.Wanted = &apiserver.WantedRow{
				ID: detail.Wanted.ID, Monitored: detail.Wanted.Monitored,
				RequiredCount: detail.Wanted.RequiredCount, PresentCount: detail.Wanted.PresentCount,
				State: detail.Wanted.State, LastSearchedAt: detail.Wanted.LastSearchedAt,
			}
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
		if detail.File != nil {
			row.File = &apiserver.PartFileRow{
				MediaObjectID: detail.File.MediaObjectID, AudioCodec: detail.File.AudioCodec,
				VideoCodec: detail.File.VideoCodec, Container: detail.File.Container,
				BitrateKbps: detail.File.BitrateKbps, Lossless: detail.File.Lossless,
				Extension: detail.File.Extension, RelPath: detail.File.RelPath, SizeBytes: detail.File.SizeBytes, Resolution: detail.File.Resolution,
				HDR: detail.File.HDR, AudioChannels: detail.File.AudioChannels, VideoBitrateKbps: detail.File.VideoBitrateKbps,
				AudioLanguages: detail.File.AudioLanguages, Subtitles: detail.File.Subtitles,
				FrameRate: detail.File.FrameRate, CRF: detail.File.CRF,
			}
		}
		rows[index] = row
	}
	return rows
}
