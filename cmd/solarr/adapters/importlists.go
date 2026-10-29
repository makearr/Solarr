package adapters

import (
	"context"
	"errors"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/service/importlist"
)

// ImportLists satisfies apiserver.ImportListSync over the import list service.
type ImportLists struct {
	Service importlist.Service
	// Forgetter drops a deleted list's record (library.ImportLists).
	Forgetter interface {
		Forget(ctx context.Context, listID int64) error
	}
	Queue *jobs.Queue
}

var _ apiserver.ImportListSync = ImportLists{}

// Types implements apiserver.ImportListSync: the library's type's list types, none for a type without import lists.
func (a ImportLists) Types(categoryID int64) (apiserver.ImportListTypes, error) {
	category, ok := a.Service.Libraries.Category(categoryID)
	if !ok {
		return apiserver.ImportListTypes{}, settings.ErrNotFound
	}
	types := apiserver.ImportListTypes{Types: []apiserver.ImportListTypeRow{}, MonitorOptions: []apiserver.MediaChoice{}}
	if module, ok := a.Service.Media.Module(media.Key(category.MediaType)); ok {
		types.MonitorOptions = choiceRows(module.Descriptor().MonitorOptions)
	}
	resolver, ok := media.Capability[media.ListResolving](a.Service.Media, media.Key(category.MediaType))
	if !ok {
		return types, nil
	}
	for _, kind := range a.Service.Catalogue.Kinds(resolver.ImportListTypes()) {
		row := apiserver.ImportListTypeRow{Type: string(kind.Type), Label: kind.Label, Help: kind.Help, Fields: []apiserver.ImportListFieldRow{}}
		for _, field := range kind.Fields {
			row.Fields = append(row.Fields, apiserver.ImportListFieldRow{
				Key: field.Key, Label: field.Label, Help: field.Help, Kind: string(field.Kind), Default: field.Default,
				Required: field.Required, Choices: choiceRows(field.Choices),
			})
		}
		types.Types = append(types.Types, row)
	}
	return types, nil
}

func choiceRows(choices []media.Choice) []apiserver.MediaChoice {
	rows := make([]apiserver.MediaChoice, len(choices))
	for i, choice := range choices {
		rows[i] = apiserver.MediaChoice{Key: choice.Key, Label: choice.Label}
	}
	return rows
}

// Check implements apiserver.ImportListSync.
func (a ImportLists) Check(categoryID int64, request settings.ImportListRequest) error {
	return a.Service.Check(categoryID, request)
}

// Status implements apiserver.ImportListSync.
func (a ImportLists) Status(ctx context.Context, list settings.ImportList) (apiserver.ImportListStatus, error) {
	previewed, err := a.Service.IsPreviewed(ctx, list)
	if err != nil {
		return apiserver.ImportListStatus{}, err
	}
	state, err := a.Service.Records.State(ctx, list.ID)
	if err != nil {
		return apiserver.ImportListStatus{}, err
	}
	status := apiserver.ImportListStatus{Previewed: previewed, LastRunAdded: int(state.RunAdded), LastRunFailure: state.RunFailure}
	if state.RunAt > 0 {
		status.LastRunAt = time.Unix(state.RunAt, 0)
	}
	return status, nil
}

// Preview implements apiserver.ImportListSync, classifying why a list could not be read.
func (a ImportLists) Preview(ctx context.Context, listID int64) ([]apiserver.ImportListPreviewRow, error) {
	judged, err := a.Service.Preview(ctx, listID)
	switch failure := importlist.FailureOf(err); {
	case err == nil:
	case errors.Is(err, settings.ErrNotFound):
		return nil, err
	case failure == importlist.FailureNotSupported:
		return nil, apiserver.ErrImportListNotSupported
	case failure == importlist.FailureOther:
		return nil, err
	default:
		return nil, apiserver.ImportListReadError{Failure: string(failure)}
	}
	rows := make([]apiserver.ImportListPreviewRow, len(judged))
	for i, entry := range judged {
		key := entry.Entry.Key()
		rows[i] = apiserver.ImportListPreviewRow{
			Title: entry.Entry.Title, PosterURL: entry.Entry.PosterURL, Provider: key.Provider, ExternalID: key.ExternalID,
			Verdict: string(entry.Verdict), Year: entry.Entry.Year, MatchProvider: entry.Match.Provider,
			MatchExternalID: entry.Match.ExternalID, WorkID: entry.WorkID,
		}
	}
	return rows, nil
}

// Run implements apiserver.ImportListSync: the list's own run, queued.
func (a ImportLists) Run(ctx context.Context, listID int64) error {
	_, err := importlist.Enqueue(ctx, a.Queue, listID)
	return err
}

// Forget implements apiserver.ImportListSync.
func (a ImportLists) Forget(ctx context.Context, listID int64) error {
	return a.Forgetter.Forget(ctx, listID)
}
