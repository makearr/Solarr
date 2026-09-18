package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
)

// RemoveAndBlocker runs ADR-53 section 3's compound action. Defined here and
// implemented by the caller, so this package never imports the library store
// directly (WorksReader's own doc comment explains the same choice).
type RemoveAndBlocker interface {
	RemoveAndBlockFile(ctx context.Context, mediaObjectID int64, req RemoveAndBlockInput) (RemoveAndBlockOutput, error)
}

// RemoveAndBlockInput is one request's own four independently-requestable steps.
type RemoveAndBlockInput struct {
	Quarantine            bool
	QuarantineReason      string
	QuarantineRetainHours int
	Block                 bool
	OSHash, QuickHash     string
	BlockReason           string
	BlockNote, BlockTitle string
	ReleaseGroup          string
	Blocklist             bool
	IndexerID             *int64
	GUID, ContentHash     string
	BlocklistTitle        string
	DesiredItemID         *int64
	Reopen                bool
}

// RemoveAndBlockOutput is what actually happened.
type RemoveAndBlockOutput struct {
	QuarantinedPaths   []string
	CrossDeviceDeletes int
	BlockID            *int64
	BlocklistID        *int64
	ReopenedItems      []int64
}

// ErrNoSuchFile reports a media_object id that does not exist -- distinct
// from sql.ErrNoRows so this package's handler does not have to know which
// underlying query produced it.
var ErrNoSuchFile = errors.New("apiserver: no such file")

// routeRemoveAndBlockFile parses the mediaObjectId path parameter, mirroring routeResolveUnidentified.
func (s *Server) routeRemoveAndBlockFile(w http.ResponseWriter, r *http.Request) {
	mediaObjectID, err := strconv.ParseInt(chi.URLParam(r, "mediaObjectId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a media object id must be a number")
		return
	}
	s.RemoveAndBlockFile(w, r, mediaObjectID)
}

// RemoveAndBlockFile runs ADR-53 section 3's action for one file. Administrator only.
func (s *Server) RemoveAndBlockFile(w http.ResponseWriter, r *http.Request, mediaObjectID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.removeAndBlocker == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "remove and block is not available")
		return
	}

	var body gen.RemoveAndBlockFileJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	result, err := s.removeAndBlocker.RemoveAndBlockFile(r.Context(), mediaObjectID, removeAndBlockInputFromBody(body))
	if err != nil {
		if errors.Is(err, ErrNoSuchFile) || errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such file")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, removeAndBlockResultView(result))
}

func removeAndBlockInputFromBody(body gen.RemoveAndBlockRequest) RemoveAndBlockInput {
	input := RemoveAndBlockInput{
		Quarantine:            derefOr(body.Quarantine, false),
		QuarantineReason:      string(derefOr(body.QuarantineReason, gen.RemoveAndBlockRequestQuarantineReasonUserDelete)),
		QuarantineRetainHours: derefOr(body.QuarantineRetainHours, 168),
		Block:                 derefOr(body.Block, false),
		OSHash:                derefOr(body.Oshash, ""),
		QuickHash:             derefOr(body.QuickHash, ""),
		BlockReason:           string(derefOr(body.BlockReason, gen.RemoveAndBlockRequestBlockReasonFAKE)),
		BlockNote:             derefOr(body.BlockNote, ""),
		BlockTitle:            derefOr(body.BlockTitle, ""),
		ReleaseGroup:          derefOr(body.ReleaseGroup, ""),
		Blocklist:             derefOr(body.Blocklist, false),
		IndexerID:             body.IndexerId,
		GUID:                  derefOr(body.Guid, ""),
		ContentHash:           derefOr(body.ContentHash, ""),
		BlocklistTitle:        derefOr(body.BlocklistTitle, ""),
		DesiredItemID:         body.DesiredItemId,
		Reopen:                derefOr(body.Reopen, false),
	}
	return input
}

func removeAndBlockResultView(result RemoveAndBlockOutput) gen.RemoveAndBlockResult {
	view := gen.RemoveAndBlockResult{
		QuarantinedPaths: result.QuarantinedPaths, CrossDeviceDeletes: result.CrossDeviceDeletes,
		ReopenedItems: result.ReopenedItems,
	}
	if view.QuarantinedPaths == nil {
		view.QuarantinedPaths = []string{}
	}
	if view.ReopenedItems == nil {
		view.ReopenedItems = []int64{}
	}
	view.BlockId = result.BlockID
	view.BlocklistId = result.BlocklistID
	return view
}
