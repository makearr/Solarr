package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/makearr/Solarr/cmd/solarr/adapters"
	"github.com/makearr/Solarr/internal/api/apiserver"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/importing/importjob"
	"github.com/makearr/Solarr/internal/jobs"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/internal/platform/config"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/scanning/scan"
	"github.com/makearr/Solarr/internal/service/webhook"
)

// inboundWebhookAdmin implements apiserver.InboundWebhooks over this process.
type inboundWebhookAdmin struct{ p *process }

var _ apiserver.InboundWebhooks = inboundWebhookAdmin{}

func inboundInfoOf(hook settings.InboundWebhook) apiserver.InboundInfo {
	info := apiserver.InboundInfo{
		ID: hook.ID, Name: hook.Name, Enabled: hook.Enabled, Actions: hook.Actions, CategoryIDs: hook.CategoryIDs, MediaServerID: hook.MediaServerID,
	}
	if hook.LastUsedAt > 0 {
		info.LastUsedAt = time.Unix(hook.LastUsedAt, 0)
	}
	return info
}

func validInboundWebhook(input apiserver.InboundInput) config.Problems {
	var problems config.Problems
	if input.Name == "" {
		problems = append(problems, config.Problem{Path: "name", Expectation: "a name"})
	}
	if len(input.Actions) == 0 {
		problems = append(problems, config.Problem{Path: "actions", Expectation: "at least one action"})
	}
	for _, action := range input.Actions {
		if !webhook.KnownAction(action) {
			problems = append(problems, config.Problem{Path: "actions", Value: action, Expectation: "an action Solarr names"})
		}
	}
	return problems
}

// List implements apiserver.InboundWebhooks.
func (a inboundWebhookAdmin) List(ctx context.Context) ([]apiserver.InboundInfo, error) {
	hooks, err := a.p.cache.InboundWebhooks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]apiserver.InboundInfo, len(hooks))
	for index, hook := range hooks {
		out[index] = inboundInfoOf(hook)
	}
	return out, nil
}

// Save implements apiserver.InboundWebhooks; adding makes the token, a replace keeps it.
func (a inboundWebhookAdmin) Save(ctx context.Context, id int64, input apiserver.InboundInput) (apiserver.InboundCreated, error) {
	if problems := validInboundWebhook(input); len(problems) > 0 {
		return apiserver.InboundCreated{}, problems
	}
	hook := settings.InboundWebhook{
		ID: id, Name: input.Name, Enabled: input.Enabled, Actions: slices.Clone(input.Actions), CategoryIDs: input.CategoryIDs, MediaServerID: input.MediaServerID,
	}
	var created apiserver.InboundCreated
	if id == 0 {
		token, hash, err := webhook.NewRandomToken()
		if err != nil {
			return apiserver.InboundCreated{}, err
		}
		hook.TokenHash, created.Token = hash, token
	} else if _, err := a.p.cache.InboundWebhookByID(ctx, id); err != nil {
		return apiserver.InboundCreated{}, err
	}
	saved, err := a.p.cache.SaveInboundWebhook(ctx, hook, clock.NewSystem().Now().Unix())
	if err != nil {
		return apiserver.InboundCreated{}, err
	}
	stored, err := a.p.cache.InboundWebhookByID(ctx, saved)
	if err != nil {
		return apiserver.InboundCreated{}, err
	}
	created.Info = inboundInfoOf(stored)
	return created, nil
}

// Rotate implements apiserver.InboundWebhooks.
func (a inboundWebhookAdmin) Rotate(ctx context.Context, id int64) (apiserver.InboundCreated, error) {
	if _, err := a.p.cache.InboundWebhookByID(ctx, id); err != nil {
		return apiserver.InboundCreated{}, err
	}
	token, hash, err := webhook.NewRandomToken()
	if err != nil {
		return apiserver.InboundCreated{}, err
	}
	if err := a.p.cache.SetInboundWebhookToken(ctx, id, hash); err != nil {
		return apiserver.InboundCreated{}, err
	}
	stored, err := a.p.cache.InboundWebhookByID(ctx, id)
	if err != nil {
		return apiserver.InboundCreated{}, err
	}
	return apiserver.InboundCreated{Info: inboundInfoOf(stored), Token: token}, nil
}

// Delete implements apiserver.InboundWebhooks.
func (a inboundWebhookAdmin) Delete(ctx context.Context, id int64) error {
	if _, err := a.p.cache.InboundWebhookByID(ctx, id); err != nil {
		return err
	}
	return a.p.cache.DeleteInboundWebhook(ctx, id)
}

// Authenticate implements apiserver.InboundWebhooks: an unknown id, a wrong token and a disabled hook are all false.
func (a inboundWebhookAdmin) Authenticate(ctx context.Context, id int64, token string) (apiserver.InboundInfo, bool) {
	hook, err := a.p.cache.InboundWebhookByID(ctx, id)
	valid := err == nil && hook.Enabled
	// A miss still hashes the token, so the answer does not time the lookup.
	matches := webhook.TokenMatches(hook.TokenHash, token)
	if !valid || !matches {
		return apiserver.InboundInfo{}, false
	}
	_ = a.p.cache.TouchInboundWebhook(ctx, id, clock.NewSystem().Now().Unix())
	return inboundInfoOf(hook), true
}

func (info inboundGrant) allows(categoryID int64) bool {
	return len(info.CategoryIDs) == 0 || slices.Contains(info.CategoryIDs, categoryID)
}

// inboundGrant is the part of a hook that says which libraries it reaches.
type inboundGrant apiserver.InboundInfo

func refused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", apiserver.ErrInboundRefused, fmt.Sprintf(format, args...))
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", apiserver.ErrInvalidChoice, fmt.Sprintf(format, args...))
}

// Run implements apiserver.InboundWebhooks: each action enqueues the job its button enqueues and nothing else.
func (a inboundWebhookAdmin) Run(ctx context.Context, hook apiserver.InboundInfo, request apiserver.InboundRequest) (apiserver.InboundResult, error) {
	grant := inboundGrant(hook)
	mode := a.p.solarrMode()
	var result jobs.EnqueueResult
	var err error
	switch webhook.Action(request.Action) {
	case webhook.ActionScan:
		result, err = a.scan(ctx, grant, request)
	case webhook.ActionImport:
		if mode.LibrariesReadOnly() {
			return apiserver.InboundResult{}, refused("this instance is in a mode that leaves the libraries alone")
		}
		result, err = a.importFolder(ctx, grant, request)
	case webhook.ActionRefresh:
		return a.refresh(ctx, grant, request)
	case webhook.ActionSearch:
		if mode.SearchesStopped() || mode.DownloadsPaused() {
			return apiserver.InboundResult{}, refused("this instance is in a mode that does not search")
		}
		result, err = a.search(ctx, grant, request)
	case webhook.ActionPlayback:
		result, err = a.playback(ctx, hook)
	default:
		return apiserver.InboundResult{}, invalid("unknown action %q", request.Action)
	}
	if err != nil {
		return apiserver.InboundResult{}, err
	}
	return apiserver.InboundResult{JobID: result.Job.ID, Duplicate: result.Duplicate}, nil
}

// within resolves a path (symlinks included) and says it lies inside the folder; the resolved path is answered.
func (a inboundWebhookAdmin) within(folder, target string) (string, bool) {
	resolved, err := a.p.fsys.Resolve(filepath.Clean(target))
	if err != nil {
		return "", false
	}
	root, err := a.p.fsys.Resolve(filepath.Clean(folder))
	if err != nil {
		return "", false
	}
	return resolved, fsx.Within(root, resolved)
}

func (a inboundWebhookAdmin) scan(ctx context.Context, grant inboundGrant, request apiserver.InboundRequest) (jobs.EnqueueResult, error) {
	snapshot := a.p.cache.Snapshot()
	if request.Path != "" {
		for _, category := range snapshot.Categories() {
			if !grant.allows(category.ID) {
				continue
			}
			for _, root := range snapshot.RootFoldersForCategory(category.ID) {
				resolved, inside := a.within(root.Path, request.Path)
				if !root.Online || !inside {
					continue
				}
				resolvedRoot, _ := a.p.fsys.Resolve(filepath.Clean(root.Path))
				relative, err := filepath.Rel(resolvedRoot, resolved)
				if err != nil {
					continue
				}
				if relative == "." {
					return scan.Enqueue(ctx, a.p.queue, root.ID, category.ID, false)
				}
				return scan.EnqueueDirectories(ctx, a.p.queue, root.ID, category.ID, []string{filepath.ToSlash(relative)})
			}
		}
		return jobs.EnqueueResult{}, refused("%s is not inside a library folder this webhook reaches", request.Path)
	}
	if request.LibraryID == 0 {
		return jobs.EnqueueResult{}, invalid("a scan needs library_id or path")
	}
	category, found := snapshot.Category(request.LibraryID)
	if !found || !grant.allows(category.ID) {
		return jobs.EnqueueResult{}, refused("this webhook does not reach that library")
	}
	var last jobs.EnqueueResult
	for _, root := range snapshot.RootFoldersForCategory(category.ID) {
		if !root.Online {
			continue
		}
		queued, err := scan.Enqueue(ctx, a.p.queue, root.ID, category.ID, false)
		if err != nil {
			return jobs.EnqueueResult{}, err
		}
		last = queued
	}
	return last, nil
}

func (a inboundWebhookAdmin) importFolder(ctx context.Context, grant inboundGrant, request apiserver.InboundRequest) (jobs.EnqueueResult, error) {
	if request.Path == "" {
		return jobs.EnqueueResult{}, invalid("an import needs a path")
	}
	folders, err := a.p.cache.DownloadFolders(ctx)
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	for _, folder := range folders {
		resolved, inside := a.within(folder.Path, request.Path)
		resolvedFolder, _ := a.p.fsys.Resolve(filepath.Clean(folder.Path))
		// The download folder itself is never imported whole.
		if !inside || resolved == resolvedFolder {
			continue
		}
		inspector := adapters.ImportInspector{Queue: a.p.queue, Settings: a.p.cache, FS: a.p.fsys, Workspace: a.p.config.Paths.CacheDir}
		if err := inspector.CheckSource(resolved); err != nil {
			return jobs.EnqueueResult{}, err
		}
		payload := importjob.Payload{SourcePath: resolved, Origin: library.ImportOriginWatched, NoUnverified: true}
		if len(grant.CategoryIDs) == 1 {
			payload.CategoryID = &grant.CategoryIDs[0]
		} else {
			payload.BestMatch = true
		}
		return importjob.Enqueue(ctx, a.p.queue, payload)
	}
	return jobs.EnqueueResult{}, refused("%s is not a folder inside a download folder", request.Path)
}

// refresh asks the work's own module; it queues its job itself, so no job id comes back.
func (a inboundWebhookAdmin) refresh(ctx context.Context, grant inboundGrant, request apiserver.InboundRequest) (apiserver.InboundResult, error) {
	workID := request.WorkID
	if workID == 0 && request.Provider != "" && request.ID != "" {
		found, ok, err := library.NewIdentity(a.p.library).WorkByExternalID(ctx, request.Provider, request.ID)
		if err != nil {
			return apiserver.InboundResult{}, err
		}
		if !ok {
			return apiserver.InboundResult{}, invalid("no work has that id")
		}
		workID = found
	}
	if workID == 0 {
		return apiserver.InboundResult{}, invalid("a refresh needs work_id, or provider and id")
	}
	if err := a.workReachable(ctx, grant, workID); err != nil {
		return apiserver.InboundResult{}, err
	}
	refresher := adapters.WorkRefresher{Works: library.NewWorks(a.p.library), Refreshing: adapters.MediaTypes{Registry: a.p.media}.RefreshingOf}
	duplicate, err := refresher.RefreshWork(ctx, workID)
	return apiserver.InboundResult{Duplicate: duplicate}, err
}

func (a inboundWebhookAdmin) workReachable(ctx context.Context, grant inboundGrant, workID int64) error {
	detail, err := library.NewWorks(a.p.library).Detail(ctx, workID)
	if err != nil {
		return invalid("no such work")
	}
	for _, categoryID := range detail.CategoryIDs {
		if grant.allows(categoryID) {
			return nil
		}
	}
	return refused("this webhook does not reach that work's library")
}

// search starts the hunt, which looks for every wanted item within its budget; work_id and library_id only say the sender's aim.
func (a inboundWebhookAdmin) search(ctx context.Context, grant inboundGrant, request apiserver.InboundRequest) (jobs.EnqueueResult, error) {
	switch {
	case request.WorkID != 0:
		if err := a.workReachable(ctx, grant, request.WorkID); err != nil {
			return jobs.EnqueueResult{}, err
		}
	case request.LibraryID != 0:
		if !grant.allows(request.LibraryID) {
			return jobs.EnqueueResult{}, refused("this webhook does not reach that library")
		}
	default:
		return jobs.EnqueueResult{}, invalid("a search needs work_id or library_id")
	}
	return a.p.enqueueHunt(ctx)
}

func (a inboundWebhookAdmin) playback(ctx context.Context, hook apiserver.InboundInfo) (jobs.EnqueueResult, error) {
	if hook.MediaServerID != 0 {
		server, err := a.p.cache.GetMediaServer(ctx, hook.MediaServerID)
		if err != nil || !server.Enabled || !server.SyncPlayback {
			return jobs.EnqueueResult{}, refused("the media server of this webhook does not sync watch state")
		}
		return a.p.queuePlaybackSync(ctx, server.ID, false)
	}
	servers, err := a.p.cache.MediaServers(ctx)
	if err != nil {
		return jobs.EnqueueResult{}, err
	}
	var last jobs.EnqueueResult
	for _, server := range servers {
		if server.Enabled && server.SyncPlayback {
			if last, err = a.p.queuePlaybackSync(ctx, server.ID, false); err != nil {
				return jobs.EnqueueResult{}, err
			}
		}
	}
	return last, nil
}
