package apiserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/identify/quality"
	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/platform/paginate"
)

// WorkDetailReader reads one work's full detail (WorksReader's own doc
// comment explains why this package never imports the library store).
type WorkDetailReader interface {
	// GetWorkDetail answers a missing id with an error wrapping sql.ErrNoRows.
	GetWorkDetail(ctx context.Context, id int64) (WorkDetailRow, error)
	// WorkPlaces answers the root folders and libraries a work is in, what deciding who may see it needs.
	WorkPlaces(ctx context.Context, id int64) (rootFolderIDs, categoryIDs []int64, err error)
	// ListWorkParts answers one page of a work's parts, of one group or of all with group "".
	ListWorkParts(ctx context.Context, id int64, group string, after paginate.TextCursor, limit int) ([]PartRow, error)
}

// GroupRow is one group of a work: its ref, its label and its wanted item.
type GroupRow struct {
	Ref    string
	Label  string
	Wanted WantedRow
}

// ExternalIDRow is one provider's id for a work, edition or part.
type ExternalIDRow struct {
	Provider   string
	ExternalID string
}

// EditionRow is one edition of a work.
type EditionRow struct {
	ID              int64
	Kind            string
	Label           string
	Year            *int64
	Format          *string
	CatalogueNumber *string
	Barcode         *string
	RuntimeMs       *int64
	IsDefault       bool
	ExternalIDs     []ExternalIDRow
}

// PartRow is one part (track, episode, chapter, ...) of a work.
type PartRow struct {
	ID          int64
	MediaRef    string
	SortKey     string
	Title       *string
	RuntimeMs   *int64
	Date        *string
	Monitored   bool
	ExternalIDs []ExternalIDRow
	// Wanted is the track's own SINGLE desired_item, nil until someone
	// explicitly wants this one track on its own (todo 4.4).
	Wanted *WantedRow
	// File is what is on disk for this part, nil when nothing is.
	File *PartFileRow
	// AvailableAt is when the part is out, zero when unknown.
	AvailableAt time.Time
	// AvailableBasis and AvailableConfidence say how that time is known and how sure it is.
	AvailableBasis, AvailableConfidence string
	// AvailableGrace is how long after that time the part is waited for before it counts as out.
	AvailableGrace time.Duration
	// Section and Number place the part as its type does: "Disc 2", "3".
	Section, Number string
	AltNumbers      map[string]string
	Credits         []CreditRow
}

// PartFileRow is the file covering one part, and what probing measured.
type PartFileRow struct {
	MediaObjectID int64
	AudioCodec    string
	VideoCodec    string
	Container     string
	BitrateKbps   int64
	Lossless      bool
	Extension     string
	RelPath       string
	SizeBytes     int64
	// What the probe measured of a video; empty for a field it did not read.
	Resolution, HDR, AudioChannels string
	VideoBitrateKbps               int64
	AudioLanguages, Subtitles      []string
	FrameRate, CRF                 float64
}

// CreditRow is one work-level credit, the person's own name included so a
// client never needs a second round-trip just to display it.
type CreditRow struct {
	PersonID   int64
	PersonName string
	Role       string
	Character  string
	ImageURL   string
}

// WantedRow is the work's own album-level SET desired_item, when one exists.
type WantedRow struct {
	ID             int64
	Monitored      bool
	RequiredCount  *int64
	PresentCount   int64
	State          string
	LastSearchedAt int64
	// NextSearchAt and AutomaticSearch are set for an album only (F2); zero and empty otherwise.
	NextSearchAt    int64
	AutomaticSearch string
	// AvailableFrom is when the next part comes out while nothing is owed yet; 0 otherwise.
	AvailableFrom int64
}

// WorkDetailRow is a work's full detail. RootFolderIDs and CategoryIDs are not
// rendered -- GetWork uses them to run ListWorks' ADR-27 visibility check
// (both halves) against one item.
type WorkDetailRow struct {
	ID            int64
	MediaType     string
	Title         string
	SortTitle     string
	Year          *int64
	RuntimeMs     *int64
	Language      *string
	Description   *string
	OnDisk        bool
	AddedAt       time.Time
	UpdatedAt     time.Time
	ExternalIDs   []ExternalIDRow
	Editions      []EditionRow
	PartCount     int64
	Credits       []CreditRow
	Genres        []string
	RootFolderIDs []int64
	CategoryIDs   []int64
	// Wanted is nil when nothing has ever wanted this album.
	Wanted *WantedRow
	// LeadID is the person the work is listed under, 0 with none; Facets the type's own fields.
	LeadID int64
	Facets map[string][]string
	Groups []GroupRow
	// Pictures are the work's provider pictures by kind (poster, fanart, logo).
	Pictures map[string]string
}

// BulkSelection is a selection as a query (section 21.7): every work visible under RootFolderIDs or CategoryIDs
// (ADR-27's two halves) that passes the list's own filters and whose id is in IDs (when non-empty) or is not in
// ExcludedIDs otherwise.
type BulkSelection struct {
	RootFolderIDs, CategoryIDs, IDs, ExcludedIDs []int64
	Fresh, Wanted, CutoffUnmet                   bool
	// Where is the list's custom filter as the JSON it carried; malformed answers ErrInvalidChoice.
	Where string
}

// WorksMonitor toggles monitored state at the album and part level (section
// 19.6). Defined here and implemented by the caller, same reasoning as
// WorksReader's own doc comment.
type WorksMonitor interface {
	// MonitorWork monitors or unmonitors what scope names and answers the work's groups after. A missing work
	// answers an error wrapping sql.ErrNoRows, an option the type does not have ErrInvalidChoice.
	MonitorWork(ctx context.Context, workID int64, scope MonitorScope) ([]GroupRow, error)
	// SetTrackWanted always ensures the track's own SINGLE desired_item
	// exists (creating it on first use, wanted or not) and sets its
	// monitored flag either way -- distinct from SetPartMonitored, which
	// only resizes the album's own required_count. Answers a missing work
	// or part id with an error wrapping sql.ErrNoRows.
	SetTrackWanted(ctx context.Context, workID, partID int64, wanted bool) (WantedRow, error)
	// BulkSetMonitored is SetWorkMonitored over a selection (section 21.7). Always returns how many works
	// matched; when preview is true nothing is changed.
	BulkSetMonitored(ctx context.Context, selection BulkSelection, monitored, preview bool) (int64, error)
	// RemoveWork drops every desired item for the work, and the work itself
	// only when no files remain for it. Never touches files. Answers a
	// missing work id with an error wrapping sql.ErrNoRows.
	// RemoveWork stops wanting an album. deleteFiles also puts its files
	// into the recycle bin (section 10.8 -- never a direct delete).
	RemoveWork(ctx context.Context, workID int64, deleteFiles, exclude bool) (RemoveWorkResult, error)
	// RemovePart stops wanting one track; deleteFiles also recycles its file.
	// Answers a part outside the work with an error wrapping sql.ErrNoRows.
	RemovePart(ctx context.Context, workID, partID int64, deleteFiles bool) (RemovePartResult, error)
}

// MonitorScope is what POST /works/{id}/monitor changes: the whole work, a group, some parts, or an option.
type MonitorScope struct {
	Monitored bool
	// Group is set for one group; "" is a work's single group.
	Group   *string
	Parts   []int64
	Option  string
	Special bool
}

// RemovePartResult is what removing a track did.
type RemovePartResult struct {
	FilesRecycled int64
}

// RemoveWorkResult is what removing an album did.
type RemoveWorkResult struct {
	WorkDeleted bool
	FilesKept   int64
	// FilesRecycled counts what deleteFiles moved out of the library.
	FilesRecycled int64
}

// routeGetWork parses the id path parameter, mirroring routeUser: a malformed
// id answers 400 the same way everywhere, before any lookup runs.
func (s *Server) routeGetWork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	s.GetWork(w, r, id)
}

// GetWork answers a work's structural detail; no scoring or Decision content
// yet (stage 4). A work under no readable root folder answers 404, same as missing.
func (s *Server) GetWork(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	if s.workDetail == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "the library is not available")
		return
	}

	row, err := s.workDetail.GetWorkDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	rootFolderIDs, categoryIDs, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !workDetailVisible(rootFolderIDs, categoryIDs, row) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return
	}

	view := buildWorkDetailView(row)
	requestRoots, requestCategories := s.visibleAt(access, settings.PermissionRequest)
	view.CanRequest = workDetailVisible(requestRoots, requestCategories, row)
	writeJSON(w, s.logger, http.StatusOK, view)
}

// annotateFileQuality marks each file against the target and cutoff of the work's first library
// the caller can see (2026-09-19: "items show whether a file satisfies the target or cutoff").
func annotateFileQuality(access authz.Access, categoryIDs []int64, parts []gen.WorkPart) {
	for _, id := range categoryIDs {
		category, err := access.Category(id, settings.PermissionRead)
		if err != nil {
			continue
		}
		for i := range parts {
			file := parts[i].File
			// A file the probe has not measured yet is not judged, rather than shown as failing.
			if file == nil || file.AudioCodec == nil && file.VideoCodec == nil {
				continue
			}
			judgement, judged := quality.Judge(access.Snapshot(), category, partFileFacts(*file))
			if !judged {
				return
			}
			meetsTarget, meetsCutoff := judgement.MeetsTarget, judgement.MeetsCutoff
			if category.QualityTarget != nil {
				file.MeetsTarget = &meetsTarget
			}
			file.MeetsCutoff = &meetsCutoff
		}
		return
	}
}

// partFileFacts is what probing measured of a file, as the facts a profile judges.
func partFileFacts(file gen.PartFile) score.Facts {
	facts := score.Facts{File: score.FileFacts{
		AudioBitrate: file.BitrateKbps * 1000, OverallBitrate: file.BitrateKbps * 1000, Lossless: file.Lossless,
		AudioCodec: derefOr(file.AudioCodec, ""), Container: derefOr(file.Container, ""),
		// The file's own type, not ffprobe's demuxer list ("mov,mp4,m4a,3gp,3g2,mj2").
		Extension:  derefOr(file.Extension, ""),
		Resolution: derefOr(file.Resolution, ""), VideoCodec: derefOr(file.VideoCodec, ""), HDR: derefOr(file.Hdr, ""),
		AudioChannels: derefOr(file.AudioChannels, ""), AudioLanguages: derefOr(file.AudioLanguages, nil),
		SubtitleLanguages: derefOr(file.Subtitles, nil), VideoBitrate: derefOr(file.VideoBitrateKbps, 0) * 1000,
		FrameRate: float64(derefOr(file.FrameRate, 0)), CRF: float64(derefOr(file.Crf, 0)),
	}}
	return facts
}

// routeSetWorkMonitored parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeSetWorkMonitored(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	s.SetWorkMonitored(w, r, id)
}

// SetWorkMonitored toggles a work's album-level monitored state. The
// request body is decoded here, not by a generated wrapper, same as
// SubscribePerson/ImportSettings (ADR-31).
func (s *Server) SetWorkMonitored(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workDetail == nil || s.worksMonitor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "monitoring is not available")
		return
	}

	var body gen.SetWorkMonitoredJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	// Monitoring decides what is searched and downloaded: request, as search itself (2026-09-28).
	if !s.workPermitted(w, r, access, id, settings.PermissionRequest) {
		return
	}

	if body.Option == nil && body.Monitored == nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "monitored is required without an option")
		return
	}
	scope := MonitorScope{
		Monitored: derefOr(body.Monitored, false), Group: body.Group, Parts: derefOr(body.Parts, nil),
		Option: derefOr(body.Option, ""), Special: derefOr(body.Specials, false),
	}
	groups, err := s.worksMonitor.MonitorWork(r.Context(), id, scope)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if scope.Monitored && derefOr(body.Search, false) {
		s.searchNowMonitored(r, id)
	}
	views := make([]gen.WorkGroup, len(groups))
	for index, group := range groups {
		views[index] = groupView(group)
	}
	writeJSON(w, s.logger, http.StatusOK, struct {
		Groups []gen.WorkGroup `json:"groups"`
	}{Groups: views})
}

// searchNowMonitored queues a search for an album just monitored (D16); failing to queue one does not
// undo the monitoring, and the hunt still finds the album within 15 minutes.
func (s *Server) searchNowMonitored(r *http.Request, id int64) {
	if s.candidateSearcher == nil {
		return
	}
	detail, err := s.workDetail.GetWorkDetail(r.Context(), id)
	if err != nil || len(detail.CategoryIDs) == 0 {
		return
	}
	if _, err := s.candidateSearcher.SearchCategory(r.Context(), detail.CategoryIDs[0], []int64{id}); err != nil {
		s.logger.Warn("search after monitoring", "work_id", id, "error", err)
	}
}

// routeSetTrackWanted parses id and partId, mirroring routeSetPartMonitored.
func (s *Server) routeSetTrackWanted(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	partID, err := strconv.ParseInt(chi.URLParam(r, "partId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a part id must be a number")
		return
	}
	s.SetTrackWanted(w, r, id, partID)
}

// SetTrackWanted wants (or stops wanting) one track on its own, independent
// of the album -- distinct from SetPartMonitored, which only resizes the
// album's own required_count (todo 4.4).
func (s *Server) SetTrackWanted(w http.ResponseWriter, r *http.Request, id, partID int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workDetail == nil || s.worksMonitor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "monitoring is not available")
		return
	}

	var body gen.SetTrackWantedJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	// Monitoring decides what is searched and downloaded: request, as search itself (2026-09-28).
	if !s.workPermitted(w, r, access, id, settings.PermissionRequest) {
		return
	}

	wanted, err := s.worksMonitor.SetTrackWanted(r.Context(), id, partID, body.Wanted)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such part")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, buildWantedView(wanted))
}

// BulkSetWorkMonitored is SetWorkMonitored over a selection (section 21.7):
// "bulk selection is a query, never a list of ids". Visibility is enforced
// by resolving rootFolderIDs and categoryIDs from the caller's own access
// before the selection is ever interpreted, the same visibility call ListWorks
// makes -- a work outside both is never matched, explicit id or not.
func (s *Server) BulkSetWorkMonitored(w http.ResponseWriter, r *http.Request) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.worksMonitor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "monitoring is not available")
		return
	}

	var body gen.BulkSetWorkMonitoredJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "the request body could not be read")
		return
	}

	rootFolderIDs, categoryIDs, err := s.visibility(access, body.CategoryId)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}

	selection := BulkSelection{
		RootFolderIDs: rootFolderIDs, CategoryIDs: categoryIDs, IDs: derefOr(body.Ids, nil), ExcludedIDs: derefOr(body.ExcludedIds, nil),
		Fresh: derefOr(body.Fresh, false), Wanted: derefOr(body.Wanted, false), CutoffUnmet: derefOr(body.CutoffUnmet, false),
		Where: derefOr(body.Where, ""),
	}
	count, err := s.worksMonitor.BulkSetMonitored(r.Context(), selection, body.Monitored, derefOr(body.Preview, false))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.BulkResult{Count: count})
}

// workVisible runs GetWork's own ADR-27 visibility check against id, writing
// a 404 (indistinguishable from missing, Invariant 37) and returning false
// when the work is not visible to the caller -- shared so a mutation can
// never be reached for a work the caller could not even read.
func (s *Server) workVisible(w http.ResponseWriter, r *http.Request, access authz.Access, id int64) bool {
	return s.workPermitted(w, r, access, id, settings.PermissionRead)
}

// workPermitted is workVisible for an action needing more than read: a work the caller can read
// but not act on answers 403, one they cannot read 404. The work's libraries decide, not the login.
func (s *Server) workPermitted(
	w http.ResponseWriter, r *http.Request, access authz.Access, id int64, required settings.Permission,
) bool {
	row, err := s.workDetail.GetWorkDetail(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return false
	}
	if err != nil {
		writeError(w, s.logger, err)
		return false
	}
	rootFolderIDs, categoryIDs := s.visibleAt(access, settings.PermissionRead)
	if !workDetailVisible(rootFolderIDs, categoryIDs, row) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return false
	}
	if required == settings.PermissionRead {
		return true
	}
	rootFolderIDs, categoryIDs = s.visibleAt(access, required)
	if !workDetailVisible(rootFolderIDs, categoryIDs, row) {
		writeError(w, s.logger, fmt.Errorf("%w: work %d needs %s", authz.ErrInsufficientPermission, id, required))
		return false
	}
	return true
}

func buildWantedView(row WantedRow) gen.Wanted {
	view := gen.Wanted{
		Id: row.ID, Monitored: row.Monitored, RequiredCount: row.RequiredCount,
		PresentCount: row.PresentCount, State: gen.WantedState(row.State),
	}
	if row.LastSearchedAt > 0 {
		view.LastSearchedAt = &row.LastSearchedAt
	}
	if row.NextSearchAt > 0 {
		view.NextSearchAt = &row.NextSearchAt
	}
	if row.AutomaticSearch != "" {
		why := gen.WantedAutomaticSearch(row.AutomaticSearch)
		view.AutomaticSearch = &why
	}
	return view
}

// workDetailVisible is GetWork's own ADR-27 visibility check: row is visible
// if either half -- on-disk root folder or desired_item category -- shares an
// id with what the caller may see. A subscription-discovered work has no root
// folder yet, so the category half is what makes it reachable at all.
func workDetailVisible(rootFolderIDs, categoryIDs []int64, row WorkDetailRow) bool {
	return sharesAnID(rootFolderIDs, row.RootFolderIDs) || sharesAnID(categoryIDs, row.CategoryIDs)
}

// sharesAnID reports whether a and b have any element in common.
func sharesAnID(a, b []int64) bool {
	seen := make(map[int64]struct{}, len(a))
	for _, id := range a {
		seen[id] = struct{}{}
	}
	for _, id := range b {
		if _, ok := seen[id]; ok {
			return true
		}
	}
	return false
}

func buildWorkDetailView(row WorkDetailRow) gen.WorkDetail {
	view := gen.WorkDetail{
		Id: row.ID, MediaType: row.MediaType, Title: row.Title, SortTitle: row.SortTitle,
		Year: yearOf(row.Year), RuntimeMs: row.RuntimeMs, Language: row.Language,
		Description: row.Description, OnDisk: row.OnDisk, LibraryIds: &row.CategoryIDs,
		AddedAt: row.AddedAt, UpdatedAt: row.UpdatedAt,
		ExternalIds: buildExternalIDViews(row.ExternalIDs),
		Editions:    buildEditionViews(row.Editions),
		PartCount:   row.PartCount,
		Credits:     buildCreditViews(row.Credits),
		Genres:      row.Genres,
	}
	if row.Wanted != nil {
		wanted := buildWantedView(*row.Wanted)
		view.Wanted = &wanted
	}
	if row.LeadID != 0 {
		view.LeadId = &row.LeadID
	}
	facets := row.Facets
	if facets == nil {
		facets = map[string][]string{}
	}
	groups := make([]gen.WorkGroup, len(row.Groups))
	for index, group := range row.Groups {
		groups[index] = groupView(group)
	}
	view.Facets, view.Groups = &facets, &groups
	if len(row.Pictures) > 0 {
		view.Pictures = &row.Pictures
	}
	return view
}

func nonEmptyStrings(values []string) *[]string {
	if len(values) == 0 {
		return nil
	}
	return &values
}

// ListWorkParts answers one page of a work's parts, the work seen as GetWork sees it.
func (s *Server) ListWorkParts(w http.ResponseWriter, r *http.Request, id int64, params gen.ListWorkPartsParams) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.workDetail == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "the library is not available")
		return
	}
	cursor, err := paginate.DecodeText(derefOr(params.Cursor, ""))
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", err.Error())
		return
	}
	roots, categories, err := s.workDetail.WorkPlaces(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return
	}
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	visibleRoots, visibleCategories, err := s.visibility(access, nil)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !workDetailVisible(visibleRoots, visibleCategories, WorkDetailRow{RootFolderIDs: roots, CategoryIDs: categories}) {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
		return
	}
	limit := paginate.ClampLimit(derefOr(params.Limit, 0))
	rows, err := s.workDetail.ListWorkParts(r.Context(), id, derefOr(params.Group, ""), cursor, limit+1)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.WorkPartPage{}
	if len(rows) > limit {
		last := rows[limit-1]
		next := paginate.TextCursor{Sort: last.SortKey, ID: last.ID}.Encode()
		page.NextCursor = &next
		rows = rows[:limit]
	}
	page.Items = buildPartViews(rows)
	annotateFileQuality(access, categories, page.Items)
	writeJSON(w, s.logger, http.StatusOK, page)
}

func (s *Server) routeListWorkParts(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "a work id must be a number")
		return
	}
	query := r.URL.Query()
	var params gen.ListWorkPartsParams
	if raw := query.Get("group"); raw != "" {
		params.Group = &raw
	}
	if raw := query.Get("cursor"); raw != "" {
		params.Cursor = &raw
	}
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "limit must be a number")
			return
		}
		params.Limit = &limit
	}
	s.ListWorkParts(w, r, id, params)
}

func buildCreditViews(rows []CreditRow) []gen.Credit {
	views := make([]gen.Credit, len(rows))
	for index, row := range rows {
		views[index] = gen.Credit{
			PersonId: row.PersonID, PersonName: row.PersonName, Role: row.Role,
			Character: strPtr(row.Character), ImageUrl: strPtr(row.ImageURL),
		}
	}
	return views
}

func buildExternalIDViews(rows []ExternalIDRow) []gen.ExternalID {
	views := make([]gen.ExternalID, len(rows))
	for index, row := range rows {
		views[index] = gen.ExternalID{Provider: row.Provider, ExternalId: row.ExternalID}
	}
	return views
}

func buildEditionViews(rows []EditionRow) []gen.Edition {
	views := make([]gen.Edition, len(rows))
	for index, row := range rows {
		year := yearOf(row.Year)
		views[index] = gen.Edition{
			Id: row.ID, Kind: row.Kind, Label: row.Label, Year: year,
			Format: row.Format, CatalogueNumber: row.CatalogueNumber, Barcode: row.Barcode,
			RuntimeMs: row.RuntimeMs, IsDefault: row.IsDefault,
			ExternalIds: buildExternalIDViews(row.ExternalIDs),
		}
	}
	return views
}

func buildPartViews(rows []PartRow) []gen.WorkPart {
	views := make([]gen.WorkPart, len(rows))
	for index, row := range rows {
		views[index] = gen.WorkPart{
			Id: row.ID, MediaRef: row.MediaRef, SortKey: row.SortKey, Title: row.Title,
			RuntimeMs: row.RuntimeMs, Date: row.Date, Monitored: row.Monitored,
			ExternalIds: buildExternalIDViews(row.ExternalIDs),
		}
		if row.Wanted != nil {
			wanted := buildWantedView(*row.Wanted)
			views[index].Wanted = &wanted
		}
		if row.File != nil {
			views[index].File = partFileView(*row.File)
		}
		if !row.AvailableAt.IsZero() {
			views[index].AvailableAt = &row.AvailableAt
		}
		if row.AvailableBasis != "" {
			views[index].AvailableBasis = &row.AvailableBasis
			confidence := gen.WorkPartAvailableConfidence(row.AvailableConfidence)
			views[index].AvailableConfidence = &confidence
			hours := int64(row.AvailableGrace / time.Hour)
			views[index].OutGraceHours = &hours
		}
		views[index].Section, views[index].Number = strPtr(row.Section), strPtr(row.Number)
		if len(row.AltNumbers) > 0 {
			views[index].AltNumbers = &row.AltNumbers
		}
		if len(row.Credits) > 0 {
			credits := buildCreditViews(row.Credits)
			views[index].Credits = &credits
		}
	}
	return views
}

// partFileView is one part's file as the API shows it; a measure the probe did not take is absent.
func partFileView(file PartFileRow) *gen.PartFile {
	view := &gen.PartFile{
		MediaObjectId: file.MediaObjectID, AudioCodec: strPtr(file.AudioCodec),
		VideoCodec: strPtr(file.VideoCodec), Container: strPtr(file.Container),
		BitrateKbps: file.BitrateKbps, Lossless: file.Lossless,
		Extension: strPtr(file.Extension), Path: strPtr(file.RelPath), Resolution: strPtr(file.Resolution), Hdr: strPtr(file.HDR),
		AudioChannels: strPtr(file.AudioChannels), AudioLanguages: nonEmptyStrings(file.AudioLanguages),
		Subtitles: nonEmptyStrings(file.Subtitles),
	}
	if file.SizeBytes > 0 {
		view.SizeBytes = &file.SizeBytes
	}
	if file.VideoBitrateKbps > 0 {
		view.VideoBitrateKbps = &file.VideoBitrateKbps
	}
	if file.FrameRate > 0 {
		rate := float32(file.FrameRate)
		view.FrameRate = &rate
	}
	if file.CRF > 0 {
		crf := float32(file.CRF)
		view.Crf = &crf
	}
	return view
}

func (s *Server) routeRemoveWork(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	s.RemoveWork(w, r, id)
}

// RemoveWork stops wanting an album (revision 7.3). Administrator only, and
// deliberately a POST rather than a DELETE: the files stay, and so does the
// album itself when any of them are still in the library.
func (s *Server) RemoveWork(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.worksMonitor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "works are not available")
		return
	}
	// An absent body means "stop wanting it, keep the files" -- what this
	// endpoint has always done, so an old caller is unchanged.
	var body struct {
		DeleteFiles bool `json:"delete_files"`
		Exclude     bool `json:"exclude"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body)
	}

	result, err := s.worksMonitor.RemoveWork(r.Context(), id, body.DeleteFiles, body.Exclude)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such work")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.RemoveWorkResult{
		WorkDeleted: result.WorkDeleted, FilesKept: result.FilesKept,
		FilesRecycled: &result.FilesRecycled,
	})
}

func (s *Server) routeRemovePart(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a work id must be a number")
		return
	}
	partID, err := strconv.ParseInt(chi.URLParam(r, "partId"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest,
			"Bad request", "a part id must be a number")
		return
	}
	s.RemovePart(w, r, id, partID)
}

// RemovePart stops wanting one track, RemoveWork's shape one level down.
func (s *Server) RemovePart(w http.ResponseWriter, r *http.Request, id, partID int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.worksMonitor == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable,
			"Unavailable", "works are not available")
		return
	}
	// An absent body keeps the file, matching RemoveWork.
	var body gen.RemovePartJSONRequestBody
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSetupBody)).Decode(&body)
	}
	deleteFiles := body.DeleteFiles != nil && *body.DeleteFiles

	result, err := s.worksMonitor.RemovePart(r.Context(), id, partID, deleteFiles)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such track")
			return
		}
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.RemovePartResult{FilesRecycled: result.FilesRecycled})
}

func groupView(group GroupRow) gen.WorkGroup {
	view := gen.WorkGroup{
		Ref: group.Ref, Label: group.Label, DesiredItemId: group.Wanted.ID, Monitored: group.Wanted.Monitored,
		PresentCount: group.Wanted.PresentCount, RequiredCount: group.Wanted.RequiredCount,
		State: gen.WorkGroupState(group.Wanted.State),
	}
	if group.Wanted.LastSearchedAt != 0 {
		at := time.Unix(group.Wanted.LastSearchedAt, 0).UTC()
		view.LastSearchedAt = &at
	}
	if group.Wanted.AvailableFrom != 0 {
		at := time.Unix(group.Wanted.AvailableFrom, 0).UTC()
		view.NextOut = &at
	}
	return view
}
