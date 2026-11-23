package apiserver

import (
	"context"
	"fmt"
	"net/http"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/db/settings"
)

// maxDefinitionBody is the largest definition text a request may carry: the file limit and some JSON around it.
const maxDefinitionBody = 300 << 10

// DefinitionProblem is one thing wrong with a definition file.
type DefinitionProblem struct {
	Severity, Code, Field string
	Line, Column          int
}

// DefinitionSetting is a value a definition asks its owner for; IsSet says one is stored.
type DefinitionSetting struct {
	Name, Label, Type string
	Required, IsSet   bool
}

// DefinitionInfo is one row of Settings > Providers.
type DefinitionInfo struct {
	Kind, Key, Name, Description, Engine, Origin, State, File string
	Hosts, MediaTypes                                         []string
	Problems                                                  []DefinitionProblem
	Settings                                                  []DefinitionSetting
}

// DefinitionText is the YAML of a definition; Builtin is the shipped text when a custom file overrides it.
type DefinitionText struct {
	Text, Builtin string
	Problems      []DefinitionProblem
}

// DefinitionTestRecord is one record a test found.
type DefinitionTestRecord struct {
	Title, Overview string
	Year            int
	IDs             map[string]string
	Item            bool
}

// DefinitionTestRequest is one request a test made; never a header or a query.
type DefinitionTestRequest struct {
	Method, Host, Path string
	Status, Millis     int
}

// DefinitionTest is what a test of a definition found.
type DefinitionTest struct {
	OK             bool
	ErrorCode, Err string
	Records        []DefinitionTestRecord
	Requests       []DefinitionTestRequest
}

// ProviderDefinitions is the administration of definition files; cmd/solarr/app implements it over the catalog. A kind or
// key that names nothing answers an error wrapping settings.ErrNotFound.
type ProviderDefinitions interface {
	List(ctx context.Context, kind string) ([]DefinitionInfo, error)
	Text(ctx context.Context, kind, key string) (DefinitionText, error)
	Put(ctx context.Context, kind, key, text string, dryRun bool) (saved bool, problems []DefinitionProblem, err error)
	Remove(ctx context.Context, kind, key string) error
	SetEnabled(ctx context.Context, kind, key string, enabled bool) error
	SetSettings(ctx context.Context, kind, key string, values map[string]string) error
	Test(ctx context.Context, kind, key, title string, year int) (DefinitionTest, error)
	Reload(ctx context.Context) error
}

func (s *Server) definitions(w http.ResponseWriter) ProviderDefinitions {
	if s.providerDefinitions == nil {
		writeError(w, s.logger, fmt.Errorf("%w: provider definitions", settings.ErrNotFound))
		return nil
	}
	return s.providerDefinitions
}

func problemViews(problems []DefinitionProblem) []gen.ProviderDefinitionProblem {
	views := make([]gen.ProviderDefinitionProblem, len(problems))
	for index, problem := range problems {
		view := gen.ProviderDefinitionProblem{Severity: gen.ProviderDefinitionProblemSeverity(problem.Severity), Code: problem.Code, Line: problem.Line, Column: problem.Column}
		if problem.Field != "" {
			view.Field = &problem.Field
		}
		views[index] = view
	}
	return views
}

// ListProviderDefinitions lists the definitions of a kind.
func (s *Server) ListProviderDefinitions(w http.ResponseWriter, r *http.Request, params gen.ListProviderDefinitionsParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	found, err := definitions.List(r.Context(), string(params.Kind))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	views := make([]gen.ProviderDefinition, len(found))
	for index, one := range found {
		view := gen.ProviderDefinition{
			Kind: gen.ProviderDefinitionKind(one.Kind), Key: one.Key, Name: one.Name, Description: one.Description,
			Engine: gen.ProviderDefinitionEngine(one.Engine), Origin: gen.ProviderDefinitionOrigin(one.Origin), State: gen.ProviderDefinitionState(one.State),
			Hosts: orEmpty(one.Hosts), MediaTypes: orEmpty(one.MediaTypes), Problems: problemViews(one.Problems), Settings: []gen.ProviderDefinitionSetting{},
		}
		for _, setting := range one.Settings {
			view.Settings = append(view.Settings, gen.ProviderDefinitionSetting{Name: setting.Name, Label: setting.Label, Type: setting.Type, Required: setting.Required, IsSet: setting.IsSet})
		}
		if one.File != "" {
			view.File = &one.File
		}
		views[index] = view
	}
	writeJSON(w, s.logger, http.StatusOK, views)
}

// GetProviderDefinition answers a definition's YAML.
func (s *Server) GetProviderDefinition(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	text, err := definitions.Text(r.Context(), string(kind), key)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.ProviderDefinitionText{Text: text.Text, Problems: problemViews(text.Problems)}
	if text.Builtin != "" {
		view.BuiltinText = &text.Builtin
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// PutProviderDefinition validates a definition and saves it as a custom file when it has no error.
func (s *Server) PutProviderDefinition(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string, params gen.PutProviderDefinitionParams) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	var body gen.ProviderDefinitionWrite
	if !s.decodeBody(w, r, maxDefinitionBody, &body, "expected a JSON object with text") {
		return
	}
	saved, problems, err := definitions.Put(r.Context(), string(kind), key, body.Text, params.DryRun != nil && *params.DryRun)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	writeJSON(w, s.logger, http.StatusOK, gen.ProviderDefinitionWriteResult{Saved: saved, Problems: problemViews(problems)})
}

// DeleteProviderDefinition moves the custom file aside.
func (s *Server) DeleteProviderDefinition(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	if err := definitions.Remove(r.Context(), string(kind), key); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) switchDefinition(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string, enabled bool) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	if err := definitions.SetEnabled(r.Context(), string(kind), key, enabled); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// EnableProviderDefinition switches a definition on and, for a custom provider file, approves its hosts.
func (s *Server) EnableProviderDefinition(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string) {
	s.switchDefinition(w, r, kind, key, true)
}

// DisableProviderDefinition switches a definition off.
func (s *Server) DisableProviderDefinition(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string) {
	s.switchDefinition(w, r, kind, key, false)
}

// PutProviderDefinitionSettings stores the owner's values of a definition's settings.
func (s *Server) PutProviderDefinitionSettings(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	var body gen.ProviderDefinitionSettingsWrite
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with values") {
		return
	}
	if err := definitions.SetSettings(r.Context(), string(kind), key, body.Values); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestProviderDefinition runs a search and the first item through a definition.
func (s *Server) TestProviderDefinition(w http.ResponseWriter, r *http.Request, kind gen.ProviderDefinitionKind, key string) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	var body gen.ProviderDefinitionTestRequest
	if !s.decodeBody(w, r, maxSetupBody, &body, "expected a JSON object with title") {
		return
	}
	result, err := definitions.Test(r.Context(), string(kind), key, body.Title, derefOr(body.Year, 0))
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	view := gen.ProviderDefinitionTestResult{Ok: result.OK, Records: []gen.ProviderDefinitionTestRecord{}, Requests: []gen.ProviderDefinitionTestRequestMade{}}
	if result.ErrorCode != "" {
		view.ErrorCode, view.Error = &result.ErrorCode, &result.Err
	}
	for _, record := range result.Records {
		item := record.Item
		view.Records = append(view.Records, gen.ProviderDefinitionTestRecord{Title: record.Title, Year: record.Year, Overview: &record.Overview, Ids: record.IDs, Item: &item})
	}
	for _, request := range result.Requests {
		view.Requests = append(view.Requests, gen.ProviderDefinitionTestRequestMade{Method: request.Method, Host: request.Host, Path: request.Path, Status: request.Status, Millis: request.Millis})
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// ReloadProviderDefinitions reads the custom folders again.
func (s *Server) ReloadProviderDefinitions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdministrator(w, r) {
		return
	}
	definitions := s.definitions(w)
	if definitions == nil {
		return
	}
	if err := definitions.Reload(r.Context()); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
