package importlist

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/media"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
)

// Bounds on what one pass may cost: lookups by name are the expensive part (an AniList entry is a Skyhook search).
const (
	previewResolutions = 30
	runResolutions     = 100
	runAdds            = 50
	// unresolvedRetry is how long an entry no source knew waits before it is looked up again.
	unresolvedRetry = 7 * 24 * time.Hour
)

// ErrNotSupported reports a list a library cannot take: its type offers no import lists, or not of that kind.
var ErrNotSupported = errors.New("importlist: this library does not take this list")

// Verdict is what a pass made of one entry.
type Verdict string

// The verdicts; only VerdictAdd is added, and only by a run.
const (
	VerdictAdd Verdict = "add"
	// VerdictHandled is an entry the list added, found held or found foreign on an earlier run.
	VerdictHandled   Verdict = "handled"
	VerdictHeld      Verdict = "held"
	VerdictExcluded  Verdict = "excluded"
	VerdictFiltered  Verdict = "filtered"
	VerdictForeign   Verdict = "foreign"
	VerdictUnknown   Verdict = "unresolved"
	VerdictUnchecked Verdict = "unchecked"
)

// Judged is one entry and what a pass made of it; Match is the id it is added by, WorkID the library's work if held.
type Judged struct {
	Entry   media.ListEntry
	Verdict Verdict
	Match   media.ProviderID
	WorkID  int64
}

// Lists are the import lists as settings keeps them.
type Lists interface {
	ImportList(ctx context.Context, id int64) (settings.ImportList, error)
	EnabledImportLists(ctx context.Context) ([]settings.ImportList, error)
}

// Records are what the lists handled and their runs (library.ImportLists).
type Records interface {
	Entries(ctx context.Context, listID int64) ([]library.ImportListEntry, error)
	Record(ctx context.Context, entries []library.ImportListEntry) error
	State(ctx context.Context, listID int64) (library.ImportListState, error)
	RecordPreview(ctx context.Context, listID int64, at time.Time, previewed string) error
	RecordRun(ctx context.Context, listID int64, at time.Time, added int, failure string) error
}

// Exclusions are the works people removed and asked not to see again (library.Exclusions).
type Exclusions interface {
	List(ctx context.Context, mediaType string) ([]library.WorkExclusion, error)
}

// Works finds the library's work a provider id names.
type Works interface {
	WorkByProviderID(ctx context.Context, mediaType, provider, externalID string) (int64, bool, error)
}

// Libraries answers a library by id.
type Libraries interface {
	Category(id int64) (settings.Category, bool)
}

// Service previews and syncs import lists, through each media type's ListResolving and Adding.
type Service struct {
	Lists      Lists
	Records    Records
	Exclusions Exclusions
	Works      Works
	Libraries  Libraries
	Media      *media.Registry
	Catalogue  Catalogue
	Clock      clock.Clock
	Logger     *slog.Logger
	// AfterAdd runs once a run added something, so the library's counts follow; nil does nothing.
	AfterAdd func(ctx context.Context)
}

func (s Service) logger() *slog.Logger {
	if s.Logger == nil {
		return slog.Default()
	}
	return s.Logger
}

// target is a list with what a pass over it needs.
type target struct {
	list     settings.ImportList
	category settings.Category
	resolver media.ListResolving
	kind     Kind
	options  Options
}

func (s Service) target(list settings.ImportList) (target, error) {
	category, ok := s.Libraries.Category(list.CategoryID)
	if !ok {
		return target{}, fmt.Errorf("%w: no such library", ErrNotSupported)
	}
	resolver, ok := media.Capability[media.ListResolving](s.Media, media.Key(category.MediaType))
	if !ok || !slices.Contains(resolver.ImportListTypes(), media.ListType(list.Type)) {
		return target{}, ErrNotSupported
	}
	kind, ok := s.Catalogue.Kind(media.ListType(list.Type))
	if !ok {
		return target{}, ErrNotSupported
	}
	options, err := DecodeOptions(list.Options)
	if err != nil {
		return target{}, err
	}
	return target{list: list, category: category, resolver: resolver, kind: kind, options: options}, nil
}

// Previewed is what a preview of the list records, and what enabling it checks: its type and settings.
func Previewed(list settings.ImportList) string {
	options, err := DecodeOptions(list.Options)
	if err != nil {
		return ""
	}
	return list.Type + "\n" + EncodeOptions(options)
}

// Check refuses a list the library cannot take: an unknown type, settings the type does not ask for, a monitor option
// the media type does not have.
func (s Service) Check(categoryID int64, request settings.ImportListRequest) error {
	category, ok := s.Libraries.Category(categoryID)
	if !ok {
		return fmt.Errorf("%w: category %d", settings.ErrNotFound, categoryID)
	}
	var accepted []media.ListType
	if resolver, ok := media.Capability[media.ListResolving](s.Media, media.Key(category.MediaType)); ok {
		accepted = resolver.ImportListTypes()
	}
	options, err := DecodeOptions(request.Options)
	if err != nil {
		return config.Problems{{Path: "options", Expectation: "settings by name"}}
	}
	problems := s.Catalogue.Validate(accepted, media.ListType(request.Type), options)
	if request.MonitorOption != "" {
		module, _ := s.Media.Module(media.Key(category.MediaType))
		if module == nil || !slices.ContainsFunc(module.Descriptor().MonitorOptions, func(choice media.Choice) bool {
			return choice.Key == request.MonitorOption
		}) {
			problems = append(problems, config.Problem{Path: "monitor_option", Value: request.MonitorOption, Expectation: "one of the library's monitor options"})
		}
	}
	return problems.OrNil()
}

// IsPreviewed reports whether the list's current type and settings are what its last preview read.
func (s Service) IsPreviewed(ctx context.Context, list settings.ImportList) (bool, error) {
	state, err := s.Records.State(ctx, list.ID)
	if err != nil {
		return false, err
	}
	return state.PreviewedAt > 0 && state.PreviewedOptions == Previewed(list), nil
}

// Preview answers what the list would add now, writing nothing but the note that it was previewed (ADR-38).
func (s Service) Preview(ctx context.Context, listID int64) ([]Judged, error) {
	list, err := s.Lists.ImportList(ctx, listID)
	if err != nil {
		return nil, err
	}
	aimed, err := s.target(list)
	if err != nil {
		return nil, err
	}
	judged, err := s.judge(ctx, aimed, previewResolutions)
	if err != nil {
		return nil, err
	}
	if err := s.Records.RecordPreview(ctx, list.ID, s.Clock.Now(), Previewed(list)); err != nil {
		return nil, err
	}
	return judged, nil
}

// judge reads the list and decides every entry, looking up at most bound entries by name.
func (s Service) judge(ctx context.Context, aimed target, bound int) ([]Judged, error) {
	entries, err := aimed.kind.Source.Entries(ctx, aimed.options)
	if err != nil {
		return nil, err
	}
	handled, err := s.handled(ctx, aimed.list.ID)
	if err != nil {
		return nil, err
	}
	excluded, err := s.excluded(ctx, aimed.category.MediaType)
	if err != nil {
		return nil, err
	}
	filters := filterTokens(aimed.list.Exclusions)
	mediaType := aimed.category.MediaType
	judged := make([]Judged, 0, len(entries))
	resolutions := 0
	seen := map[media.ProviderID]bool{}
	for _, entry := range entries {
		key := entry.Key()
		if key == (media.ProviderID{}) || seen[key] {
			continue
		}
		seen[key] = true
		verdict := Judged{Entry: entry}
		switch {
		case handled[key]:
			verdict.Verdict = VerdictHandled
		case filtered(filters, entry.Title, entry.IDs):
			verdict.Verdict = VerdictFiltered
		case anyExcluded(excluded, entry.IDs):
			verdict.Verdict = VerdictExcluded
		default:
			workID, held, err := s.held(ctx, mediaType, entry.IDs)
			if err != nil {
				return nil, err
			}
			switch {
			case held:
				verdict.Verdict, verdict.WorkID = VerdictHeld, workID
			case resolutions == bound:
				verdict.Verdict = VerdictUnchecked
			default:
				resolutions++
				match, err := aimed.resolver.ResolveListEntry(ctx, entry)
				if err != nil {
					return nil, err
				}
				verdict.Match = match.ProviderID
				if err := s.decide(ctx, &verdict, match, excluded, filters, mediaType); err != nil {
					return nil, err
				}
			}
		}
		judged = append(judged, verdict)
	}
	return judged, nil
}

// decide judges a resolved entry by the id it is added by, which the entry's own ids may not hold.
func (s Service) decide(ctx context.Context, verdict *Judged, match media.ListMatch, excluded map[media.ProviderID]bool, filters []string, mediaType string) error {
	matched := []media.ProviderID{match.ProviderID}
	switch {
	case match.Foreign:
		verdict.Verdict = VerdictForeign
	case !match.Found:
		verdict.Verdict = VerdictUnknown
	case anyExcluded(excluded, matched):
		verdict.Verdict = VerdictExcluded
	case filtered(filters, "", matched):
		verdict.Verdict = VerdictFiltered
	default:
		workID, held, err := s.held(ctx, mediaType, matched)
		if err != nil {
			return err
		}
		verdict.Verdict = VerdictAdd
		if held {
			verdict.Verdict, verdict.WorkID = VerdictHeld, workID
		}
	}
	return nil
}

// handled answers the entries the list need not look at again: added, held or foreign, and unknown until retried.
func (s Service) handled(ctx context.Context, listID int64) (map[media.ProviderID]bool, error) {
	entries, err := s.Records.Entries(ctx, listID)
	if err != nil {
		return nil, err
	}
	retryBefore := s.Clock.Now().Add(-unresolvedRetry).Unix()
	handled := make(map[media.ProviderID]bool, len(entries))
	for _, entry := range entries {
		if entry.Outcome == library.ListUnresolved && entry.SeenAt < retryBefore {
			continue
		}
		handled[media.ProviderID{Provider: entry.Provider, ExternalID: entry.ExternalID}] = true
	}
	return handled, nil
}

func (s Service) excluded(ctx context.Context, mediaType string) (map[media.ProviderID]bool, error) {
	rows, err := s.Exclusions.List(ctx, mediaType)
	if err != nil {
		return nil, err
	}
	excluded := make(map[media.ProviderID]bool, len(rows))
	for _, row := range rows {
		excluded[media.ProviderID{Provider: row.Provider, ExternalID: row.ExternalID}] = true
	}
	return excluded, nil
}

func (s Service) held(ctx context.Context, mediaType string, ids []media.ProviderID) (int64, bool, error) {
	for _, id := range ids {
		if id.ExternalID == "" {
			continue
		}
		workID, held, err := s.Works.WorkByProviderID(ctx, mediaType, id.Provider, id.ExternalID)
		if err != nil || held {
			return workID, held, err
		}
	}
	return 0, false, nil
}

func anyExcluded(excluded map[media.ProviderID]bool, ids []media.ProviderID) bool {
	return slices.ContainsFunc(ids, func(id media.ProviderID) bool { return excluded[id] })
}

// filterTokens reads a list's exclusions: titles or provider:id, separated by commas or lines.
func filterTokens(exclusions string) []string {
	var tokens []string
	for _, token := range strings.FieldsFunc(exclusions, func(r rune) bool { return r == ',' || r == '\n' }) {
		if token = strings.ToLower(strings.TrimSpace(token)); token != "" {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

func filtered(tokens []string, title string, ids []media.ProviderID) bool {
	if title != "" && slices.Contains(tokens, strings.ToLower(strings.TrimSpace(title))) {
		return true
	}
	return slices.ContainsFunc(ids, func(id media.ProviderID) bool {
		return slices.Contains(tokens, strings.ToLower(id.Provider+":"+id.ExternalID))
	})
}
