package adapters

import (
	"context"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// WantedParts satisfies apiserver.WantedPartsReader over library.Works, placing each part as its type does.
type WantedParts struct {
	Works      library.Works
	Categories Categories
	// PartPosition and GroupLabel are the library's type's numbering and group names.
	PartPosition func(mediaType, ref string) (section, number string)
	GroupLabel   func(mediaType, ref string) string
	Clock        clock.Clock
}

// Missing answers a page of a library's missing parts.
func (a WantedParts) Missing(ctx context.Context, categoryID int64, after paginate.TextCursor, limit int) ([]apiserver.WantedPartRow, error) {
	parts, err := a.Works.WantedParts(ctx, categoryID, library.WantedPartsCursor{Date: after.Sort, ID: after.ID}, int64(limit), a.Clock.Now())
	if err != nil {
		return nil, err
	}
	return a.rows(categoryID, parts), nil
}

// Held answers every part of a library with a measured file.
func (a WantedParts) Held(ctx context.Context, categoryID int64) ([]apiserver.WantedPartRow, error) {
	parts, err := a.Works.HeldParts(ctx, categoryID)
	if err != nil {
		return nil, err
	}
	return a.rows(categoryID, parts), nil
}

// rows places each part; its group is the ref its own scopes it under ("s03" of "s03:e07").
func (a WantedParts) rows(categoryID int64, parts []library.WantedPart) []apiserver.WantedPartRow {
	mediaType, _ := a.Categories.MediaTypeOf(categoryID)
	rows := make([]apiserver.WantedPartRow, len(parts))
	for i, part := range parts {
		group, _, _ := strings.Cut(part.MediaRef, ":")
		_, number := a.PartPosition(mediaType, part.MediaRef)
		rows[i] = apiserver.WantedPartRow{
			PartID: part.PartID, WorkID: part.WorkID, WorkTitle: part.WorkTitle,
			GroupRef: group, GroupLabel: a.GroupLabel(mediaType, group), Number: number, Title: part.Title, Date: part.Date,
		}
		if part.AvailableAt > 0 {
			rows[i].AvailableAt = time.Unix(part.AvailableAt, 0).UTC()
		}
		if part.File != nil {
			file := part.File
			rows[i].File = &apiserver.PartFileRow{
				MediaObjectID: file.MediaObjectID, AudioCodec: file.AudioCodec, VideoCodec: file.VideoCodec, Container: file.Container,
				BitrateKbps: file.BitrateKbps, Lossless: file.Lossless, SizeBytes: file.SizeBytes, Resolution: file.Resolution,
				HDR: file.HDR, AudioChannels: file.AudioChannels, VideoBitrateKbps: file.VideoBitrateKbps,
				AudioLanguages: file.AudioLanguages, Subtitles: file.Subtitles, FrameRate: file.FrameRate, CRF: file.CRF,
			}
		}
	}
	return rows
}
