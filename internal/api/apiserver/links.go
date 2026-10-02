package apiserver

import (
	"context"
	"net/http"

	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// LinkRow is one path that would become a link to another.
type LinkRow struct {
	MediaObjectID, SizeBytes int64
	Keep, Replace, Method    string
}

// LinkSkip counts the paths left alone for one reason.
type LinkSkip struct {
	Reason string
	Count  int
}

// LinkPreview is what linking one library's identical copies would do.
type LinkPreview struct {
	Links            []LinkRow
	Total            int
	ReclaimableBytes int64
	Skipped          []LinkSkip
}

// LinkManager previews and queues the linking of a library's identical copies.
type LinkManager interface {
	PreviewLinks(ctx context.Context, categoryID int64) (LinkPreview, error)
	StartLinks(ctx context.Context, categoryID int64) error
}

// linkPreviewRows bounds the pairs one preview lists; the totals cover them all.
const linkPreviewRows = 200

func (s *Server) routeGetCategoryLinks(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.GetCategoryLinks(w, r, id)
	}
}

func (s *Server) routeLinkCategoryCopies(w http.ResponseWriter, r *http.Request) {
	if id, err := parsePathID(w, s, r, "id", "a category id must be a number"); err == nil {
		s.LinkCategoryCopies(w, r, id)
	}
}

// GetCategoryLinks answers the plan; a library the caller cannot read is 404.
func (s *Server) GetCategoryLinks(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.links == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "linking is not available")
		return
	}
	preview, err := s.links.PreviewLinks(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	type linkView struct {
		MediaObjectID int64  `json:"media_object_id"`
		SizeBytes     int64  `json:"size_bytes"`
		Keep          string `json:"keep"`
		Replace       string `json:"replace"`
		Method        string `json:"method"`
	}
	type skipView struct {
		Reason string `json:"reason"`
		Count  int    `json:"count"`
	}
	view := struct {
		Total            int        `json:"total"`
		ReclaimableBytes int64      `json:"reclaimable_bytes"`
		Links            []linkView `json:"links"`
		Skipped          []skipView `json:"skipped"`
	}{Total: preview.Total, ReclaimableBytes: preview.ReclaimableBytes, Links: []linkView{}, Skipped: []skipView{}}
	for index, row := range preview.Links {
		if index == linkPreviewRows {
			break
		}
		view.Links = append(view.Links, linkView(row))
	}
	for _, skip := range preview.Skipped {
		view.Skipped = append(view.Skipped, skipView(skip))
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// LinkCategoryCopies queues the run; it needs manage access, and a library the caller cannot read is 404.
func (s *Server) LinkCategoryCopies(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionManage); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.links == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "linking is not available")
		return
	}
	if err := s.links.StartLinks(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
