package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/makearr/Solarr/internal/acquiring/indexer/spec"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/metadata/specprovider"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/providerdef"
)

// definitionsAdmin implements apiserver.ProviderDefinitions over the catalog, the custom folders and the stored states.
type definitionsAdmin struct{ p *process }

var _ apiserver.ProviderDefinitions = definitionsAdmin{}

// loadReports are what the last reload found wrong with each custom file, by kind.
type loadReports struct {
	mu    sync.Mutex
	files map[providerdef.Kind][]providerdef.FileProblems
}

func (r *loadReports) set(kind providerdef.Kind, reports []providerdef.FileProblems) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.files == nil {
		r.files = map[providerdef.Kind][]providerdef.FileProblems{}
	}
	r.files[kind] = reports
}

func (r *loadReports) of(kind providerdef.Kind) []providerdef.FileProblems {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.files[kind])
}

func notFound(what string) error { return fmt.Errorf("%w: %s", settings.ErrNotFound, what) }

func problemViews(problems []providerdef.Problem) []apiserver.DefinitionProblem {
	views := make([]apiserver.DefinitionProblem, len(problems))
	for index, problem := range problems {
		views[index] = apiserver.DefinitionProblem{Severity: string(problem.Severity), Code: string(problem.Code), Field: problem.Field, Line: problem.Line, Column: problem.Column}
	}
	return views
}

func hasErrorProblem(problems []providerdef.Problem) bool {
	return slices.ContainsFunc(problems, func(problem providerdef.Problem) bool { return problem.Severity == providerdef.SeverityError })
}

func validKind(kind string) (providerdef.Kind, error) {
	if slices.Contains(providerdef.Kinds, providerdef.Kind(kind)) {
		return providerdef.Kind(kind), nil
	}
	return "", notFound("definition kind " + kind)
}

// List implements apiserver.ProviderDefinitions.
func (a definitionsAdmin) List(ctx context.Context, kindName string) ([]apiserver.DefinitionInfo, error) {
	kind, err := validKind(kindName)
	if err != nil {
		return nil, err
	}
	states := a.p.definitionStates(kind)
	reports := a.p.definitionReports.of(kind)
	var out []apiserver.DefinitionInfo
	listed := map[string]bool{}
	for _, def := range a.p.definitions.Catalog().List(kind) {
		info := apiserver.DefinitionInfo{
			Kind: kindName, Key: def.Key, Name: def.Name, Description: def.Description, Engine: string(def.Engine), Origin: "builtin",
			State: string(providerdef.StatusOf(def, states[def.Key])), Hosts: def.NormalHosts(), MediaTypes: def.MediaTypes,
		}
		if def.Source == providerdef.SourceCustom {
			info.Origin = "custom"
			if def.Overrides {
				info.Origin = "override"
			}
		}
		for _, report := range reports {
			if strings.TrimSuffix(report.File, ".yaml") == def.Key {
				info.Problems = append(info.Problems, problemViews(report.Problems)...)
			}
		}
		info.Settings = a.settingsOf(ctx, kind, def)
		listed[def.Key] = true
		out = append(out, info)
	}
	for _, report := range reports {
		key := strings.TrimSuffix(report.File, ".yaml")
		if listed[key] || !hasErrorProblem(report.Problems) {
			continue
		}
		out = append(out, apiserver.DefinitionInfo{Kind: kindName, Key: key, Name: key, Engine: "", Origin: "custom", State: "error", File: report.File, Problems: problemViews(report.Problems)})
	}
	return out, nil
}

// settingsOf lists what a metadata definition asks its owner for, and whether each value is stored.
func (a definitionsAdmin) settingsOf(ctx context.Context, kind providerdef.Kind, def providerdef.Definition) []apiserver.DefinitionSetting {
	if kind != providerdef.KindMetadata || def.Engine != providerdef.EngineHTTP {
		return nil
	}
	parsed, err := specprovider.Parse(def.Key, def.Name, def.Raw)
	if err != nil {
		return nil
	}
	held, _ := a.p.cache.ProviderDefinitionSettings(ctx, string(kind), def.Key)
	var out []apiserver.DefinitionSetting
	for _, one := range parsed.Settings {
		out = append(out, apiserver.DefinitionSetting{Name: one.Name, Label: one.Label, Type: one.Type, Required: one.Required, IsSet: held[one.Name] != ""})
	}
	return out
}

// Text implements apiserver.ProviderDefinitions.
func (a definitionsAdmin) Text(_ context.Context, kindName, key string) (apiserver.DefinitionText, error) {
	kind, err := validKind(kindName)
	if err != nil {
		return apiserver.DefinitionText{}, err
	}
	builtin := string(a.p.builtinDefinitions()[kind][key])
	custom, readErr := a.p.providerDefinitions().ReadCustom(kind, key)
	if errors.Is(readErr, fs.ErrNotExist) || readErr != nil {
		if builtin == "" {
			return apiserver.DefinitionText{}, notFound("definition " + key)
		}
		return apiserver.DefinitionText{Text: builtin}, nil
	}
	_, problems := providerdef.Parse(kind, key+".yaml", custom, providerdef.SourceCustom)
	return apiserver.DefinitionText{Text: string(custom), Builtin: builtin, Problems: problemViews(problems)}, nil
}

// checkBody validates what only the engine behind a definition can: its own schema.
func checkBody(kind providerdef.Kind, def providerdef.Definition) []providerdef.Problem {
	var err error
	switch {
	case kind == providerdef.KindMetadata && def.Engine == providerdef.EngineHTTP:
		_, err = specprovider.Parse(def.Key, def.Name, def.Raw)
	case kind == providerdef.KindIndexer && def.Engine == providerdef.EngineSpec:
		_, err = spec.FromYAML(def.Raw)
	}
	if err != nil {
		return []providerdef.Problem{{Severity: providerdef.SeverityError, Code: providerdef.CodeBadBody, Field: err.Error()}}
	}
	return nil
}

// Put implements apiserver.ProviderDefinitions.
func (a definitionsAdmin) Put(_ context.Context, kindName, key, text string, dryRun bool) (bool, []apiserver.DefinitionProblem, error) {
	kind, err := validKind(kindName)
	if err != nil {
		return false, nil, err
	}
	def, problems := providerdef.Parse(kind, key+".yaml", []byte(text), providerdef.SourceCustom)
	if !hasErrorProblem(problems) {
		problems = append(problems, checkBody(kind, def)...)
	}
	if hasErrorProblem(problems) || dryRun {
		return false, problemViews(problems), nil
	}
	if err := a.p.providerDefinitions().WriteCustom(kind, key, []byte(text)); err != nil {
		return false, nil, err
	}
	a.p.reloadProviderDefinitions()
	return true, problemViews(problems), nil
}

// Remove implements apiserver.ProviderDefinitions.
func (a definitionsAdmin) Remove(ctx context.Context, kindName, key string) error {
	kind, err := validKind(kindName)
	if err != nil {
		return err
	}
	if err := a.p.providerDefinitions().RemoveCustom(kind, key, clock.NewSystem().Now().Unix()); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return notFound("custom definition " + key)
		}
		return err
	}
	if err := a.p.cache.ClearProviderDefinitionState(ctx, kindName, key); err != nil {
		return err
	}
	a.p.reloadProviderDefinitions()
	return nil
}

// SetEnabled implements apiserver.ProviderDefinitions: switching a custom provider file on is its approval.
func (a definitionsAdmin) SetEnabled(ctx context.Context, kindName, key string, enabled bool) error {
	kind, err := validKind(kindName)
	if err != nil {
		return err
	}
	def, found := a.p.definitions.Catalog().Get(kind, key)
	if !found {
		return notFound("definition " + key)
	}
	now := clock.NewSystem().Now().Unix()
	if enabled && def.Source == providerdef.SourceCustom && def.Engine != providerdef.EngineBuiltin {
		if err := a.p.cache.ApproveProviderDefinition(ctx, kindName, key, def.Hash, def.NormalHosts(), now); err != nil {
			return err
		}
	}
	if err := a.p.cache.SetProviderDefinitionEnabled(ctx, kindName, key, &enabled, now); err != nil {
		return err
	}
	a.p.reloadProviderDefinitions()
	return nil
}

// SetSettings implements apiserver.ProviderDefinitions: an empty value clears one, a missing name keeps what is stored.
func (a definitionsAdmin) SetSettings(ctx context.Context, kindName, key string, values map[string]string) error {
	kind, err := validKind(kindName)
	if err != nil {
		return err
	}
	def, found := a.p.definitions.Catalog().Get(kind, key)
	if !found {
		return notFound("definition " + key)
	}
	declared := map[string]bool{}
	for _, one := range a.settingsOf(ctx, kind, def) {
		declared[one.Name] = true
	}
	merged, err := a.p.cache.ProviderDefinitionSettings(ctx, kindName, key)
	if err != nil {
		return err
	}
	if merged == nil {
		merged = map[string]string{}
	}
	for name, value := range values {
		if !declared[name] {
			return config.Problems{{Path: "settings." + name, Value: name, Expectation: "a setting the definition declares"}}
		}
		if value == "" {
			delete(merged, name)
		} else {
			merged[name] = value
		}
	}
	if err := a.p.cache.SetProviderDefinitionSettings(ctx, kindName, key, merged, clock.NewSystem().Now().Unix()); err != nil {
		return err
	}
	a.p.reloadProviderDefinitions()
	return nil
}

// recorder notes each request a test makes: method, host, path and the answer, never a query, header or body.
type recorder struct {
	inner    specprovider.Doer
	requests *[]apiserver.DefinitionTestRequest
}

func (r recorder) Do(ctx context.Context, request *http.Request) (*http.Response, error) {
	wall := clock.NewSystem()
	started := wall.Now()
	response, err := r.inner.Do(ctx, request)
	made := apiserver.DefinitionTestRequest{Method: request.Method, Host: request.URL.Host, Path: request.URL.Path, Millis: int(wall.Now().Sub(started).Milliseconds())}
	if response != nil {
		made.Status = response.StatusCode
	}
	*r.requests = append(*r.requests, made)
	return response, err
}

// Test implements apiserver.ProviderDefinitions: a search for the title, then the first item.
func (a definitionsAdmin) Test(ctx context.Context, kindName, key, title string, year int) (apiserver.DefinitionTest, error) {
	kind, err := validKind(kindName)
	if err != nil {
		return apiserver.DefinitionTest{}, err
	}
	if kind != providerdef.KindMetadata {
		return apiserver.DefinitionTest{OK: false, ErrorCode: "not_supported", Err: "only metadata definitions can be tested here"}, nil
	}
	held, ok := a.p.records.entry(key)
	if !ok {
		if _, found := a.p.definitions.Catalog().Get(kind, key); !found {
			return apiserver.DefinitionTest{}, notFound("definition " + key)
		}
		return apiserver.DefinitionTest{OK: false, ErrorCode: "not_active", Err: "switch the definition on (approving its hosts) before testing it"}, nil
	}
	var result apiserver.DefinitionTest
	provider := specprovider.New(held.parsed, recorder{inner: held.client, requests: &result.Requests}, clock.NewSystem(), held.values)
	query := specprovider.Query{Title: title}
	if year > 0 {
		query.Year = fmt.Sprint(year)
	}
	fail := func(err error) apiserver.DefinitionTest {
		result.ErrorCode, result.Err = "failed", err.Error()
		var failure *specprovider.Error
		if errors.As(err, &failure) {
			result.ErrorCode = string(failure.Code)
		}
		return result
	}
	found, err := provider.Search(ctx, query)
	if err != nil {
		return fail(err), nil
	}
	for _, record := range found {
		result.Records = append(result.Records, apiserver.DefinitionTestRecord{Title: record.Title, Year: record.Year, Overview: record.Overview, IDs: record.IDs})
	}
	if len(found) > 0 {
		if id := found[0].IDs[provider.Namespace()]; id != "" {
			item, err := provider.Item(ctx, id)
			if err != nil {
				return fail(err), nil
			}
			result.Records = append([]apiserver.DefinitionTestRecord{{Title: item.Title, Year: item.Year, Overview: item.Overview, IDs: item.IDs, Item: true}}, result.Records...)
		}
	}
	result.OK = true
	return result, nil
}

// Reload implements apiserver.ProviderDefinitions.
func (a definitionsAdmin) Reload(context.Context) error {
	a.p.reloadProviderDefinitions()
	return nil
}
