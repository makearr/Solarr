package apiserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

// ImportListSync is import lists as the API needs them (ADR-38): the types a library takes, the check a write passes,
// the preview, a run now. Implemented by the caller; ErrImportListNotSupported and ErrImportListUnreadable classify.
type ImportListSync interface {
	Types(categoryID int64) (ImportListTypes, error)
	Check(categoryID int64, request settings.ImportListRequest) error
	Status(ctx context.Context, list settings.ImportList) (ImportListStatus, error)
	Preview(ctx context.Context, listID int64) ([]ImportListPreviewRow, error)
	Run(ctx context.Context, listID int64) error
	// Forget drops what a deleted list did; the works it added stay.
	Forget(ctx context.Context, listID int64) error
}

// ErrImportListNotSupported reports a list its library cannot take.
var ErrImportListNotSupported = errors.New("this library does not take this list")

// ImportListReadError reports a list that could not be read, by the failure the sync names it.
type ImportListReadError struct{ Failure string }

func (e ImportListReadError) Error() string { return "the list could not be read: " + e.Failure }

// ErrImportListUnreadable matches every ImportListReadError.
var ErrImportListUnreadable = errors.New("the list could not be read")

// Is makes errors.Is(err, ErrImportListUnreadable) hold for any read failure.
func (e ImportListReadError) Is(target error) bool { return target == ErrImportListUnreadable }

// ImportListTypes are the list types a library takes and its media type's monitor options.
type ImportListTypes struct {
	Types          []ImportListTypeRow
	MonitorOptions []MediaChoice
}

// ImportListTypeRow is one list type with the settings it asks for.
type ImportListTypeRow struct {
	Type, Label, Help string
	Fields            []ImportListFieldRow
}

// ImportListFieldRow is one setting of a list type.
type ImportListFieldRow struct {
	Key, Label, Help, Kind, Default string
	Required                        bool
	Choices                         []MediaChoice
}

// ImportListStatus is a list's last preview and run; a zero LastRunAt is never.
type ImportListStatus struct {
	Previewed      bool
	LastRunAt      time.Time
	LastRunAdded   int
	LastRunFailure string
}

// ImportListPreviewRow is one entry of a preview and what enabling would do with it.
type ImportListPreviewRow struct {
	Title, PosterURL, Provider, ExternalID, Verdict string
	Year                                            int
	MatchProvider, MatchExternalID                  string
	WorkID                                          int64
}

// importListReadDetails word a read failure for the person.
var importListReadDetails = map[string]string{
	"needs_key":          "This list reads TMDB, which needs your TMDB key: add it under Settings, Metadata.",
	"list_not_found":     "The list does not exist, or it is private.",
	"source_unavailable": "The list's source did not answer; try again in a while.",
}

func (s *Server) importListID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "an id must be a number")
		return 0, false
	}
	return id, true
}

// routeListImportLists parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeListImportLists(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.importListID(w, r); ok {
		s.ListImportLists(w, r, id)
	}
}

// ListImportLists answers one library's own import lists, each with its last preview and run (ADR-38).
func (s *Server) ListImportLists(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	lists, err := s.auth.Settings.ImportLists(r.Context(), id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.ImportList, len(lists))
	for index, list := range lists {
		status, err := s.importListStatus(r.Context(), list)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		views[index] = importListView(list, status)
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

func (s *Server) importListStatus(ctx context.Context, list settings.ImportList) (ImportListStatus, error) {
	if s.importLists == nil {
		return ImportListStatus{}, nil
	}
	return s.importLists.Status(ctx, list)
}

// routeListImportListTypes parses the id path parameter.
func (s *Server) routeListImportListTypes(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.importListID(w, r); ok {
		s.ListImportListTypes(w, r, id)
	}
}

// ListImportListTypes answers the list types a library takes, with their settings; read on the library.
func (s *Server) ListImportListTypes(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if _, err := access.Category(id, settings.PermissionRead); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.importLists == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "import lists are not available")
		return
	}
	types, err := s.importLists.Types(id)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.ImportListTypes{Types: make([]gen.ImportListType, len(types.Types)), MonitorOptions: listChoiceViews(types.MonitorOptions)}
	for i, row := range types.Types {
		fields := make([]gen.ImportListField, len(row.Fields))
		for j, field := range row.Fields {
			fields[j] = gen.ImportListField{
				Key: field.Key, Label: field.Label, Help: field.Help, Kind: gen.ImportListFieldKind(field.Kind),
				Required: field.Required, Default: field.Default, Choices: listChoiceViews(field.Choices),
			}
		}
		view.Types[i] = gen.ImportListType{Type: row.Type, Label: row.Label, Help: row.Help, Fields: fields}
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

func listChoiceViews(choices []MediaChoice) []gen.MediaChoice {
	views := make([]gen.MediaChoice, len(choices))
	for i, choice := range choices {
		views[i] = gen.MediaChoice{Key: choice.Key, Label: choice.Label}
	}
	return views
}

// routeCreateImportList parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeCreateImportList(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.importListID(w, r); ok {
		s.CreateImportList(w, r, id)
	}
}

// CreateImportList adds a disabled import list to a library; it is enabled after its preview. Administrator only.
func (s *Server) CreateImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.CreateImportListJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	request := importListRequestFromWrite(body)
	if request.Enabled {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Preview first",
			"A new list starts disabled: save it, preview what it would add, then enable it.")
		return
	}
	if !s.checkImportList(w, id, request, "Nothing was created.") {
		return
	}
	created, err := s.auth.Settings.CreateImportList(r.Context(), id, request)
	if err != nil {
		s.writeImportListError(w, err, "no such library", "Nothing was created.")
		return
	}
	s.writeImportList(w, r, http.StatusCreated, created)
}

// checkImportList answers false, having written the problem, for a list the library cannot take.
func (s *Server) checkImportList(w http.ResponseWriter, categoryID int64, request settings.ImportListRequest, closing string) bool {
	if s.importLists == nil {
		return true
	}
	if err := s.importLists.Check(categoryID, request); err != nil {
		s.writeImportListError(w, err, "no such library", closing)
		return false
	}
	return true
}

func (s *Server) writeImportListError(w http.ResponseWriter, err error, missing, closing string) {
	var problems config.Problems
	switch {
	case errors.As(err, &problems):
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "The import list has problems", problems.Report("the import list", closing))
	case errors.Is(err, settings.ErrNotFound):
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", missing)
	case errors.Is(err, ErrImportListNotSupported):
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Not supported", "This library does not take this kind of list.")
	case errors.Is(err, ErrImportListUnreadable):
		var read ImportListReadError
		detail := "The list could not be read."
		if errors.As(err, &read) && importListReadDetails[read.Failure] != "" {
			detail = importListReadDetails[read.Failure]
		}
		writeProblem(w, s.logger, http.StatusBadGateway, TypeUnavailable, "The list could not be read", detail)
	default:
		writeError(w, s.logger, err)
	}
}

func (s *Server) writeImportList(w http.ResponseWriter, r *http.Request, status int, list settings.ImportList) {
	listStatus, err := s.importListStatus(r.Context(), list)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, status, importListView(list, listStatus))
}

// routeUpdateImportList parses the id path parameter, mirroring routeGetWork.
func (s *Server) routeUpdateImportList(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.importListID(w, r); ok {
		s.UpdateImportList(w, r, id)
	}
}

// UpdateImportList replaces an import list's fields; enabling needs a preview of the type and settings it is saved
// with (ADR-38). Administrator only.
func (s *Server) UpdateImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	var body gen.UpdateImportListJSONRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxImportBody)).Decode(&body); err != nil {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the request body could not be read")
		return
	}
	current, err := s.auth.Settings.ImportList(r.Context(), id)
	if err != nil {
		s.writeImportListError(w, err, "no such import list", "Nothing was changed.")
		return
	}
	request := importListRequestFromWrite(body)
	if !s.checkImportList(w, current.CategoryID, request, "Nothing was changed.") {
		return
	}
	if request.Enabled {
		status, err := s.importListStatus(r.Context(), settings.ImportList{
			ID: id, CategoryID: current.CategoryID, Type: request.Type, Options: request.Options,
		})
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		if s.importLists != nil && !status.Previewed {
			writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Preview first",
				"Preview what this list would add with these settings before enabling it.")
			return
		}
	}
	updated, err := s.auth.Settings.UpdateImportList(r.Context(), id, request)
	if err != nil {
		s.writeImportListError(w, err, "no such import list", "Nothing was changed.")
		return
	}
	s.writeImportList(w, r, http.StatusOK, updated)
}

// routePreviewImportList parses the id path parameter.
func (s *Server) routePreviewImportList(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.importListID(w, r); ok {
		s.PreviewImportList(w, r, id)
	}
}

// PreviewImportList answers what the list would add now, adding nothing (ADR-38). Administrator only.
func (s *Server) PreviewImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.importLists == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "import lists are not available")
		return
	}
	if _, err := s.auth.Settings.ImportList(r.Context(), id); err != nil {
		s.writeImportListError(w, err, "no such import list", "")
		return
	}
	rows, err := s.importLists.Preview(r.Context(), id)
	if err != nil {
		s.writeImportListError(w, err, "no such import list", "")
		return
	}
	entries := make([]gen.ImportListPreviewEntry, len(rows))
	for i, row := range rows {
		entries[i] = gen.ImportListPreviewEntry{
			Title: row.Title, Provider: row.Provider, ExternalId: row.ExternalID, Verdict: gen.ImportListPreviewEntryVerdict(row.Verdict),
			PosterUrl: strPtr(row.PosterURL), MatchProvider: strPtr(row.MatchProvider), MatchExternalId: strPtr(row.MatchExternalID),
		}
		if row.Year > 0 {
			entries[i].Year = &row.Year
		}
		if row.WorkID != 0 {
			entries[i].WorkId = &row.WorkID
		}
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ImportListPreview{Entries: entries})
}

// routeRunImportList parses the id path parameter.
func (s *Server) routeRunImportList(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.importListID(w, r); ok {
		s.RunImportList(w, r, id)
	}
}

// RunImportList queues an enabled list's run now. Administrator only.
func (s *Server) RunImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if s.importLists == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "import lists are not available")
		return
	}
	list, err := s.auth.Settings.ImportList(r.Context(), id)
	if err != nil {
		s.writeImportListError(w, err, "no such import list", "")
		return
	}
	if !list.Enabled {
		writeProblem(w, s.logger, http.StatusConflict, TypeConflict, "Disabled", "Enable the list to run it.")
		return
	}
	if err := s.importLists.Run(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// routeDeleteImportList parses the id path parameter, mirroring routeDeleteCategory.
func (s *Server) routeDeleteImportList(w http.ResponseWriter, r *http.Request) {
	if id, ok := s.importListID(w, r); ok {
		s.DeleteImportList(w, r, id)
	}
}

// DeleteImportList removes an import list and what it did; the works it added stay. Administrator only.
func (s *Server) DeleteImportList(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if err := s.auth.Settings.DeleteImportList(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	if s.importLists != nil {
		if err := s.importLists.Forget(r.Context(), id); err != nil {
			writeError(w, s.logger, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func importListView(list settings.ImportList, status ImportListStatus) gen.ImportList {
	view := gen.ImportList{
		Id: list.ID, CategoryId: list.CategoryID, Name: list.Name, Type: list.Type, Options: importListOptions(list.Options),
		CadenceHours: int(list.CadenceHours), Monitor: list.Monitor, MonitorOption: list.MonitorOption,
		QualityProfileId: list.QualityProfileID, Exclusions: list.Exclusions, Enabled: list.Enabled,
		Previewed: status.Previewed, LastRunAdded: status.LastRunAdded, LastRunFailure: gen.ImportListLastRunFailure(status.LastRunFailure),
	}
	if list.URL != "" {
		view.Url = &list.URL
	}
	if !status.LastRunAt.IsZero() {
		at := status.LastRunAt.UTC()
		view.LastRunAt = &at
	}
	return view
}

// importListOptions reads stored settings for the view; a value that is not settings by name shows none.
func importListOptions(stored string) map[string]string {
	options := map[string]string{}
	if strings.TrimSpace(stored) != "" {
		if err := json.Unmarshal([]byte(stored), &options); err != nil {
			return map[string]string{}
		}
	}
	return options
}

func importListRequestFromWrite(body gen.ImportListWrite) settings.ImportListRequest {
	request := settings.ImportListRequest{
		Name: body.Name, Type: body.Type, CadenceHours: int64(body.CadenceHours),
		QualityProfileID: body.QualityProfileId, Monitor: true,
	}
	if body.Url != nil {
		request.URL = *body.Url
	}
	if body.Exclusions != nil {
		request.Exclusions = *body.Exclusions
	}
	if body.Monitor != nil {
		request.Monitor = *body.Monitor
	}
	if body.MonitorOption != nil {
		request.MonitorOption = *body.MonitorOption
	}
	if body.Enabled != nil {
		request.Enabled = *body.Enabled
	}
	if body.Options != nil {
		kept := map[string]string{}
		for key, value := range *body.Options {
			if value = strings.TrimSpace(value); value != "" {
				kept[key] = value
			}
		}
		if len(kept) > 0 {
			encoded, _ := json.Marshal(kept) // a map of strings always encodes
			request.Options = string(encoded)
		}
	}
	return request
}
