package apiserver

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/makearr/Solarr/internal/api/apiserver/gen"
	"github.com/makearr/Solarr/internal/api/authz"
	"github.com/makearr/Solarr/internal/db/settings"
)

// WatchPair names one media server user.
type WatchPair struct {
	ServerID     int64
	ServerUserID string
}

// WorkWatchRow is what a set of server users has played of one work.
type WorkWatchRow struct {
	Finished, Started int
	LastPlayedAt      int64
}

// PartPlayRow is one user's state of one part (PartID 0 is the whole work).
type PartPlayRow struct {
	PartID                 int64
	MediaRef, Title        string
	Pair                   WatchPair
	Played                 bool
	PositionMS, DurationMS int64
	LastPlayedAt           int64
}

// WatchedWorkRow is a work on the cleanup list.
type WatchedWorkRow struct {
	ID                                 int64
	Title                              string
	Year                               int64
	LastPlayedAt, SizeBytes, FileCount int64
}

// WatchedSelection selects the cleanup list.
type WatchedSelection struct {
	RootFolderIDs, CategoryIDs []int64
	MediaTypes                 []string
	Pairs                      []WatchPair
	LastPlayedBefore, After    int64
	Limit                      int
	IDs                        []int64
}

// WatchStateReads is the watch state stored from the media servers.
type WatchStateReads interface {
	WatchOf(ctx context.Context, pairs []WatchPair, workIDs []int64) (map[int64]WorkWatchRow, error)
	PartsOf(ctx context.Context, pairs []WatchPair, workID int64) ([]PartPlayRow, error)
	WatchedWorks(ctx context.Context, selection WatchedSelection) ([]WatchedWorkRow, error)
	WorkFileIDs(ctx context.Context, workID int64) ([]int64, error)
}

// watchScope is whose watch state a caller sees: their own, everyone counted (administrators without a mapping), or none.
type watchScope struct {
	kind    string
	pairs   []WatchPair
	names   map[WatchPair]string
	counted int
}

const (
	scopeOwn      = "own"
	scopeEveryone = "everyone"
	scopeNone     = "none"
)

// watchScopeOf resolves the caller's scope from the users mapped on every connection that syncs watch state.
func (s *Server) watchScopeOf(ctx context.Context, access authz.Access) watchScope {
	scope := watchScope{kind: scopeNone, names: map[WatchPair]string{}}
	if s.watchState == nil {
		return scope
	}
	servers, err := s.auth.Settings.MediaServers(ctx)
	if err != nil {
		return scope
	}
	var own, counted []WatchPair
	for _, server := range servers {
		if !server.Enabled || !server.SyncPlayback {
			continue
		}
		users, err := s.auth.Settings.MediaServerUsers(ctx, server.ID)
		if err != nil {
			continue
		}
		for _, user := range users {
			pair := WatchPair{ServerID: server.ID, ServerUserID: user.ServerUserID}
			scope.names[pair] = user.Name
			if user.UserID != nil && *user.UserID == access.UserID() {
				own = append(own, pair)
			}
			if user.Counted {
				counted = append(counted, pair)
			}
		}
	}
	switch {
	case len(own) > 0:
		scope.kind, scope.pairs, scope.counted = scopeOwn, own, len(own)
	case access.Role() == settings.RoleAdmin && len(counted) > 0:
		scope.kind, scope.pairs, scope.counted = scopeEveryone, counted, len(counted)
	}
	return scope
}

// watchViews answers the watch status of each work for the scope; a work nobody started has no entry.
func (s *Server) watchViews(ctx context.Context, scope watchScope, ids []int64) map[int64]gen.WorkWatch {
	out := map[int64]gen.WorkWatch{}
	if scope.kind == scopeNone || len(ids) == 0 {
		return out
	}
	found, err := s.watchState.WatchOf(ctx, scope.pairs, ids)
	if err != nil {
		s.logger.Warn("watch state not read", "error", err)
		return out
	}
	for id, watch := range found {
		view := gen.WorkWatch{Status: gen.WorkWatchStatusInProgress}
		if watch.Finished > 0 {
			view.Status = gen.WorkWatchStatusYes
		}
		if watch.LastPlayedAt > 0 {
			last := watch.LastPlayedAt
			view.LastPlayedAt = &last
		}
		if scope.kind == scopeEveryone {
			by, of := watch.Finished, scope.counted
			view.WatchedBy, view.Of = &by, &of
		}
		out[id] = view
	}
	return out
}

// GetWorkPlayback answers what the caller may see of a work's watch state.
func (s *Server) GetWorkPlayback(w http.ResponseWriter, r *http.Request, id int64) {
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	if !s.workVisible(w, r, access, id) {
		return
	}
	scope := s.watchScopeOf(r.Context(), access)
	view := gen.WorkPlayback{Scope: gen.WorkPlaybackScope(scope.kind), CountedUsers: scope.counted, Parts: []gen.WorkPlaybackPart{}}
	if scope.kind != scopeNone {
		rows, err := s.watchState.PartsOf(r.Context(), scope.pairs, id)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		index := map[int64]int{}
		for _, row := range rows {
			position, seen := index[row.PartID]
			if !seen {
				position = len(view.Parts)
				index[row.PartID] = position
				view.Parts = append(view.Parts, gen.WorkPlaybackPart{PartId: row.PartID, MediaRef: row.MediaRef, Title: row.Title, Plays: []gen.WorkPlay{}})
			}
			play := gen.WorkPlay{Played: row.Played, PositionMs: row.PositionMS, DurationMs: row.DurationMS}
			if scope.kind == scopeEveryone {
				play.User = scope.names[row.Pair]
			}
			if row.LastPlayedAt > 0 {
				last := row.LastPlayedAt
				play.LastPlayedAt = &last
			}
			view.Parts[position].Plays = append(view.Parts[position].Plays, play)
		}
	}
	writeJSON(w, s.logger, http.StatusOK, view)
}

// cleanupPairs are the counted users of the connections that take this library's files or sync its watch state.
func (s *Server) cleanupPairs(ctx context.Context, categoryID int64) ([]WatchPair, error) {
	servers, err := s.auth.Settings.MediaServers(ctx)
	if err != nil {
		return nil, err
	}
	libraries, err := s.auth.Settings.AllMediaServerLibraries(ctx)
	if err != nil {
		return nil, err
	}
	var pairs []WatchPair
	for _, server := range servers {
		mapped := false
		for _, library := range libraries[server.ID] {
			mapped = mapped || library.CategoryID == categoryID
		}
		if !server.Enabled || !server.SyncPlayback || !mapped {
			continue
		}
		users, err := s.auth.Settings.MediaServerUsers(ctx, server.ID)
		if err != nil {
			return nil, err
		}
		for _, user := range users {
			if user.Counted {
				pairs = append(pairs, WatchPair{ServerID: server.ID, ServerUserID: user.ServerUserID})
			}
		}
	}
	return pairs, nil
}

func (s *Server) watchedSelection(w http.ResponseWriter, r *http.Request, categoryID int64, days int) (WatchedSelection, bool) {
	if !s.requireAdministrator(w, r) {
		return WatchedSelection{}, false
	}
	access, err := authz.FromContext(r.Context())
	if err != nil {
		writeError(w, s.logger, err)
		return WatchedSelection{}, false
	}
	if s.watchState == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "watch state is not available")
		return WatchedSelection{}, false
	}
	roots, categories, err := s.visibilityAt(access, &categoryID, settings.PermissionRead)
	if err != nil {
		writeError(w, s.logger, err)
		return WatchedSelection{}, false
	}
	pairs, err := s.cleanupPairs(r.Context(), categoryID)
	if err != nil {
		writeError(w, s.logger, err)
		return WatchedSelection{}, false
	}
	return WatchedSelection{
		RootFolderIDs: roots, CategoryIDs: categories, MediaTypes: s.libraryMediaTypes(&categoryID, false), Pairs: pairs,
		LastPlayedBefore: s.wall.Now().Add(-time.Duration(days) * 24 * time.Hour).Unix(),
	}, true
}

// ListWatchedWorks answers the cleanup list of one library.
func (s *Server) ListWatchedWorks(w http.ResponseWriter, r *http.Request, id int64, params gen.ListWatchedWorksParams) {
	selection, ok := s.watchedSelection(w, r, id, derefOr(params.Days, 30))
	if !ok {
		return
	}
	limit := min(max(derefOr(params.Limit, 50), 1), 200)
	if after := derefOr(params.Cursor, ""); after != "" {
		parsed, err := strconv.ParseInt(after, 10, 64)
		if err != nil {
			writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "the cursor is not one this list gave")
			return
		}
		selection.After = parsed
	}
	selection.Limit = limit + 1
	rows, err := s.watchState.WatchedWorks(r.Context(), selection)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	page := gen.WatchedPage{Items: []gen.WatchedWork{}, CountedUsers: len(selection.Pairs)}
	if len(rows) > limit {
		rows = rows[:limit]
		next := strconv.FormatInt(rows[len(rows)-1].ID, 10)
		page.NextCursor = &next
	}
	for _, row := range rows {
		item := gen.WatchedWork{
			WorkId: row.ID, Title: row.Title, WatchedBy: len(selection.Pairs), Of: len(selection.Pairs), SizeBytes: row.SizeBytes, FileCount: row.FileCount,
		}
		if row.Year > 0 {
			year := int(row.Year)
			item.Year = &year
		}
		if row.LastPlayedAt > 0 {
			last := row.LastPlayedAt
			item.LastPlayedAt = &last
		}
		page.Items = append(page.Items, item)
	}
	writeJSON(w, s.logger, http.StatusOK, page)
}

// RecycleWatchedWorks moves the files of the chosen works that are still on the cleanup list to the recycle bin.
func (s *Server) RecycleWatchedWorks(w http.ResponseWriter, r *http.Request, id int64) {
	var body gen.RecycleWatchedWorksJSONRequestBody
	if !s.requireAdministrator(w, r) || !s.decodeBody(w, r, maxSetupBody, &body, "expected work_ids") {
		return
	}
	selection, ok := s.watchedSelection(w, r, id, derefOr(body.Days, 30))
	if !ok {
		return
	}
	if len(body.WorkIds) == 0 {
		writeProblem(w, s.logger, http.StatusBadRequest, TypeBadRequest, "Bad request", "no works chosen")
		return
	}
	selection.IDs, selection.Limit = body.WorkIds, len(body.WorkIds)
	rows, err := s.watchState.WatchedWorks(r.Context(), selection)
	if err != nil {
		writeError(w, s.logger, err)
		return
	}
	preview := derefOr(body.Preview, false)
	if !preview && (s.removeAndBlocker == nil || derefOr(body.Unmonitor, false) && s.worksMonitor == nil) {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "recycling files is not available")
		return
	}
	result := gen.RecycleWatchedResult{}
	for _, row := range rows {
		fileIDs, err := s.watchState.WorkFileIDs(r.Context(), row.ID)
		if err != nil {
			writeError(w, s.logger, err)
			return
		}
		result.Works++
		result.Files += len(fileIDs)
		result.Bytes += row.SizeBytes
		if preview {
			continue
		}
		for _, fileID := range fileIDs {
			if _, err := s.removeAndBlocker.RemoveAndBlockFile(r.Context(), fileID, RemoveAndBlockInput{
				RecycleBin: true, RecycleReason: "user_delete", RecycleRetainHours: 168,
			}); err != nil {
				writeError(w, s.logger, err)
				return
			}
		}
		if derefOr(body.Unmonitor, false) {
			if _, err := s.worksMonitor.MonitorWork(r.Context(), row.ID, MonitorScope{Monitored: false}); err != nil {
				writeError(w, s.logger, err)
				return
			}
		}
	}
	writeJSON(w, s.logger, http.StatusOK, result)
}

// SyncMediaServerPlayback queues a read of the connection's watch state now.
func (s *Server) SyncMediaServerPlayback(w http.ResponseWriter, r *http.Request, id int64) {
	if !s.requireAdministrator(w, r) {
		return
	}
	if _, err := s.auth.Settings.GetMediaServer(r.Context(), id); err != nil {
		writeProblem(w, s.logger, http.StatusNotFound, TypeNotFound, "Not found", "no such media server")
		return
	}
	if s.syncMediaServerPlayback == nil {
		writeProblem(w, s.logger, http.StatusServiceUnavailable, TypeUnavailable, "Unavailable", "watch state is not available")
		return
	}
	if err := s.syncMediaServerPlayback(r.Context(), id); err != nil {
		writeError(w, s.logger, err)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// enumString is a generated enum's value, "" when the request left it out.
func enumString[T ~string](value *T) string {
	if value == nil {
		return ""
	}
	return string(*value)
}

// watchFilterOfScope is the status filter and the users it looks at; nothing for a caller who sees no watch state.
func watchFilterOfScope(scope watchScope, status string) (string, []WatchPair) {
	if status == "" || scope.kind == scopeNone {
		return "", nil
	}
	return status, scope.pairs
}

// watchFilterOf resolves the caller's scope and applies it to the requested status.
func (s *Server) watchFilterOf(ctx context.Context, access authz.Access, status string) (string, []WatchPair) {
	if status == "" {
		return "", nil
	}
	return watchFilterOfScope(s.watchScopeOf(ctx, access), status)
}
