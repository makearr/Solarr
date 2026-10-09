package apiserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// WantedPartRow is one part wanted at its own grain: missing, or held with the file a cutoff view judges.
type WantedPartRow struct {
	PartID, WorkID       int64
	WorkTitle            string
	GroupRef, GroupLabel string
	Number, Title, Date  string
	AvailableAt          time.Time
	File                 *PartFileRow
}

// WantedPartsReader reads Wanted at the grain of a part. Defined here, implemented by the caller.
type WantedPartsReader interface {
	// Missing answers up to limit missing parts of a library after the cursor (date, id), newest first.
	Missing(ctx context.Context, categoryID int64, after paginate.TextCursor, limit int) ([]WantedPartRow, error)
	// Held answers every part of a library with a measured file, for the cutoff view to judge.
	Held(ctx context.Context, categoryID int64) ([]WantedPartRow, error)
}

func (s *Server) routeListWantedParts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	categoryID, err := strconv.ParseInt(query.Get("category_id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "category_id is required and must be a number")
		return
	}
	params := gen.ListWantedPartsParams{CategoryId: categoryID}
	if raw := query.Get("cursor"); raw != "" {
		params.Cursor = &raw
	}
	if raw := query.Get("view"); raw != "" {
		view := gen.ListWantedPartsParamsView(raw)
		params.View = &view
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListWantedParts(w, r, params)
}

// ListWantedParts answers one page of a library's wanted parts; read on the library.
func (s *Server) ListWantedParts(w http.ResponseWriter, r *http.Request, params gen.ListWantedPartsParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	category, err := access.Category(params.CategoryId, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.wantedParts == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "wanted parts are not available")
		return
	}
	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	if derefOr(params.View, gen.ListWantedPartsParamsViewMissing) == gen.ListWantedPartsParamsViewCutoff {
		judged := category.QualityTarget != nil || category.QualityCutoffID != nil
		page := gen.WantedPartPage{Items: []gen.WantedPart{}, Judged: &judged}
		if !judged {
			writeJSON(w, s.logger, http.StatusOK, page)
			return
		}
		held, err := s.wantedParts.Held(r.Context(), params.CategoryId)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		skipped := int64(0)
		for _, row := range held {
			view := wantedPartView(row)
			judgement, ok := quality.Judge(access.Snapshot(), category, partFileFacts(*view.File))
			if !ok || judgement.MeetsCutoff {
				continue
			}
			if skipped < cursor.ID {
				skipped++
				continue
			}
			if len(page.Items) == limit {
				next := paginate.TextCursor{Sort: "offset", ID: cursor.ID + int64(limit)}.Encode()
				page.NextCursor = &next
				break
			}
			meetsTarget, meetsCutoff := judgement.MeetsTarget, judgement.MeetsCutoff
			if category.QualityTarget != nil {
				view.File.MeetsTarget = &meetsTarget
			}
			view.File.MeetsCutoff = &meetsCutoff
			page.Items = append(page.Items, view)
		}
		writeJSON(w, s.logger, http.StatusOK, page)
		return
	}
	rows, err := s.wantedParts.Missing(r.Context(), params.CategoryId, cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.WantedPartPage{Items: make([]gen.WantedPart, 0, len(rows))}
	for _, row := range rows {
		page.Items = append(page.Items, wantedPartView(row))
	}
	if len(rows) > limit {
		page.Items = page.Items[:limit]
		last := rows[limit-1]
		next := paginate.TextCursor{Sort: last.Date, ID: last.PartID}.Encode()
		page.NextCursor = &next
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

func wantedPartView(row WantedPartRow) gen.WantedPart {
	view := gen.WantedPart{
		PartId: row.PartID, WorkId: row.WorkID, WorkTitle: row.WorkTitle, GroupRef: row.GroupRef, GroupLabel: row.GroupLabel,
		Number: row.Number, Title: strPtr(row.Title), Date: strPtr(row.Date),
	}
	if !row.AvailableAt.IsZero() {
		view.AvailableAt = &row.AvailableAt
	}
	if row.File != nil {
		view.File = partFileView(*row.File)
	}
	return view
}
