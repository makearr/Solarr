// Package serverpush tells media servers about files that arrived, moved or went away: it queues paths per server and
// sends them after a debounce window.
package serverpush

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// MaxAttempts is how often a row is tried before it is dropped.
const MaxAttempts = 10

// BatchSize is how many rows one run sends.
const BatchSize = 200

// DefaultDebounce is how long a row waits, so one import is told as one batch.
const DefaultDebounce = 30 * time.Second

// freshWindow is how recent a file must be to count as new for a created push.
const freshWindow = 10 * time.Minute

// Settings is the connection side: servers, their library mapping and path pairs.
type Settings interface {
	MediaServers(ctx context.Context) ([]settings.MediaServer, error)
	AllMediaServerLibraries(ctx context.Context) (map[int64][]settings.MediaServerLibrary, error)
	MediaServerPaths(ctx context.Context, serverID int64) ([]settings.MediaServerPath, error)
	SetMediaServerResult(ctx context.Context, id, at int64, message string) error
	MediaServerPushDebounceSeconds() int64
}

// Roots answers a root folder's path.
type Roots interface {
	RootFolder(id int64) (settings.RootFolder, bool)
}

// Queue is the pending table.
type Queue interface {
	Add(ctx context.Context, rows []library.PendingPush) error
	Due(ctx context.Context, serverID, cutoff, limit int64) ([]library.PendingPush, error)
	ServersDue(ctx context.Context, cutoff int64) ([]int64, error)
	Delete(ctx context.Context, ids []int64) error
	Bump(ctx context.Context, ids []int64) error
	WorkFiles(ctx context.Context, workID int64) ([]library.WorkFilePlace, error)
	WorkTitles(ctx context.Context, ids []int64) (map[int64]string, error)
}

// Clients opens the pusher of a connection; nil with no error says the kind cannot push.
type Clients interface {
	Pusher(ctx context.Context, server settings.MediaServer) (mediaserver.Pusher, error)
}

// Scheduler asks for a push run of one server after a delay.
type Scheduler interface {
	ScheduleFlush(ctx context.Context, serverID int64, delay time.Duration) error
}

// Service queues and sends pushes.
type Service struct {
	Settings  Settings
	Roots     Roots
	Queue     Queue
	Clients   Clients
	Scheduler Scheduler
	Clock     clock.Clock
	Logger    *slog.Logger
	Debounce  time.Duration
}

func (s Service) debounce() time.Duration {
	if s.Debounce > 0 {
		return s.Debounce
	}
	if seconds := s.Settings.MediaServerPushDebounceSeconds(); seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return DefaultDebounce
}

// target is one server a library's files are pushed to.
type target struct {
	server settings.MediaServer
	pairs  []mediaserver.PathPair
}

func (s Service) targets(ctx context.Context, categoryID int64) ([]target, error) {
	servers, err := s.Settings.MediaServers(ctx)
	if err != nil {
		return nil, err
	}
	libraries, err := s.Settings.AllMediaServerLibraries(ctx)
	if err != nil {
		return nil, err
	}
	var targets []target
	for _, server := range servers {
		if !server.Enabled || !server.PushFiles {
			continue
		}
		if kind, ok := mediaserver.KindOf(server.Kind); !ok || !kind.CanPush {
			continue
		}
		mapped := slices.ContainsFunc(libraries[server.ID], func(l settings.MediaServerLibrary) bool { return l.CategoryID == categoryID })
		if !mapped {
			continue
		}
		stored, err := s.Settings.MediaServerPaths(ctx, server.ID)
		if err != nil {
			return nil, err
		}
		pairs := make([]mediaserver.PathPair, len(stored))
		for i, one := range stored {
			pairs[i] = mediaserver.PathPair{Solarr: one.SolarrPrefix, Server: one.ServerPrefix}
		}
		targets = append(targets, target{server: server, pairs: pairs})
	}
	return targets, nil
}

// RecordWork queues the folders of a work's files that are new (change created) or all of them (any other change), for
// every server that takes files of the library.
func (s Service) RecordWork(ctx context.Context, categoryID, workID int64, change string) error {
	targets, err := s.targets(ctx, categoryID)
	if err != nil || len(targets) == 0 {
		return err
	}
	places, err := s.Queue.WorkFiles(ctx, workID)
	if err != nil {
		return err
	}
	now := s.Clock.Now()
	var folders []string
	for _, place := range places {
		if change == library.PendingCreated && now.Unix()-place.CreatedAt > int64(freshWindow/time.Second) {
			continue
		}
		root, ok := s.Roots.RootFolder(place.RootFolderID)
		if !ok {
			continue
		}
		folder := path.Dir(strings.TrimRight(root.Path, "/") + "/" + place.RelPath)
		if !slices.Contains(folders, folder) {
			folders = append(folders, folder)
		}
	}
	return s.Record(ctx, categoryID, workID, change, folders)
}

// Record queues paths (Solarr's own) for every server that takes files of the library, mapped to the server's paths.
func (s Service) Record(ctx context.Context, categoryID, workID int64, change string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	targets, err := s.targets(ctx, categoryID)
	if err != nil || len(targets) == 0 {
		return err
	}
	at := s.Clock.Now().Unix()
	var rows []library.PendingPush
	for _, one := range targets {
		for _, p := range paths {
			mapped, _ := mediaserver.MapPath(one.pairs, p)
			rows = append(rows, library.PendingPush{ServerID: one.server.ID, CategoryID: categoryID, Path: mapped, Change: change, WorkID: workID, QueuedAt: at})
		}
	}
	if err := s.Queue.Add(ctx, rows); err != nil {
		return err
	}
	for _, one := range targets {
		if err := s.Scheduler.ScheduleFlush(ctx, one.server.ID, s.debounce()); err != nil {
			s.logger().Warn("could not schedule a media server push", slog.Int64("server_id", one.server.ID), slog.Any("error", err))
		}
	}
	return nil
}

// ScheduleDue asks for a run of every server with rows past the debounce window.
func (s Service) ScheduleDue(ctx context.Context) error {
	ids, err := s.Queue.ServersDue(ctx, s.Clock.Now().Add(-s.debounce()).Unix())
	if err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		failures = append(failures, s.Scheduler.ScheduleFlush(ctx, id, 0))
	}
	return errors.Join(failures...)
}

// Flush sends up to BatchSize due rows of one server. A failure leaves the rows with one more attempt; a row at
// MaxAttempts is dropped and the connection's last error says so.
func (s Service) Flush(ctx context.Context, serverID int64) error {
	servers, err := s.Settings.MediaServers(ctx)
	if err != nil {
		return err
	}
	var server settings.MediaServer
	for _, one := range servers {
		if one.ID == serverID {
			server = one
		}
	}
	rows, err := s.Queue.Due(ctx, serverID, s.Clock.Now().Add(-s.debounce()).Unix(), BatchSize)
	if err != nil || len(rows) == 0 {
		return err
	}
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	if server.ID == 0 || !server.Enabled || !server.PushFiles {
		return s.Queue.Delete(ctx, ids)
	}
	libraries, err := s.Settings.AllMediaServerLibraries(ctx)
	if err != nil {
		return err
	}
	changes := make([]mediaserver.Change, len(rows))
	for i, row := range rows {
		changes[i] = mediaserver.Change{Path: row.Path, Kind: row.Change, LibraryIDs: libraryIDs(libraries[serverID], row.CategoryID)}
	}
	pusher, err := s.Clients.Pusher(ctx, server)
	if err == nil && pusher == nil {
		err = fmt.Errorf("serverpush: %s cannot be told of files", server.Kind)
	}
	if err == nil {
		err = pusher.Changed(ctx, changes)
	}
	at := s.Clock.Now().Unix()
	if err == nil {
		if err := s.Queue.Delete(ctx, ids); err != nil {
			return err
		}
		s.announce(ctx, server, pusher, rows)
		return s.Settings.SetMediaServerResult(ctx, serverID, at, "")
	}
	if errors.Is(err, mediaserver.ErrBusy) {
		return nil
	}
	var spent, retry []int64
	for _, row := range rows {
		if row.Attempts+1 >= MaxAttempts {
			spent = append(spent, row.ID)
		} else {
			retry = append(retry, row.ID)
		}
	}
	if err := s.Queue.Bump(ctx, retry); err != nil {
		return err
	}
	message := err.Error()
	if len(spent) > 0 {
		if err := s.Queue.Delete(ctx, spent); err != nil {
			return err
		}
		message = fmt.Sprintf("%d paths dropped after %d attempts: %s", len(spent), MaxAttempts, message)
		s.logger().Warn("media server paths dropped", slog.Int64("server_id", serverID), slog.Int("count", len(spent)))
	}
	if recorded := s.Settings.SetMediaServerResult(ctx, serverID, at, message); recorded != nil {
		return recorded
	}
	s.logger().Warn("media server push failed", slog.Int64("server_id", serverID), slog.Any("error", err))
	return nil
}

func libraryIDs(mapped []settings.MediaServerLibrary, categoryID int64) []string {
	var ids []string
	for _, one := range mapped {
		if one.CategoryID == categoryID {
			ids = append(ids, one.ServerLibraryID)
		}
	}
	return ids
}

func (s Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// announce shows one "Now available" message per new work; a failure is logged, the push already succeeded.
func (s Service) announce(ctx context.Context, server settings.MediaServer, pusher mediaserver.Pusher, rows []library.PendingPush) {
	announcer, ok := pusher.(mediaserver.Announcer)
	if !ok || !server.Announce {
		return
	}
	var works []int64
	for _, row := range rows {
		if row.Change == library.PendingCreated && row.WorkID != 0 && !slices.Contains(works, row.WorkID) {
			works = append(works, row.WorkID)
		}
	}
	if len(works) == 0 {
		return
	}
	titles, err := s.Queue.WorkTitles(ctx, works)
	if err != nil {
		s.logger().Warn("could not read titles to announce", slog.Any("error", err))
		return
	}
	for _, id := range works {
		if title := titles[id]; title != "" {
			if err := announcer.Announce(ctx, "Now available: "+title); err != nil {
				s.logger().Warn("media server announcement failed", slog.Int64("server_id", server.ID), slog.Any("error", err))
				return
			}
		}
	}
}
