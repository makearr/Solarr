// Package playback reads what media server users have played and stores it by work and part.
package playback

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/mediaserver"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// PageSize is how many played items one request asks for, and how many states one transaction stores.
const PageSize = 200

// DefaultPlayedPercent counts an item as played where the server gives only a position.
const DefaultPlayedPercent = 90

// Settings is the connection side.
type Settings interface {
	MediaServers(ctx context.Context) ([]settings.MediaServer, error)
	MediaServerPaths(ctx context.Context, serverID int64) ([]settings.MediaServerPath, error)
	MediaServerUsers(ctx context.Context, serverID int64) ([]settings.MediaServerUser, error)
	SetMediaServerUsers(ctx context.Context, serverID int64, users []settings.MediaServerUser) error
	SetMediaServerResult(ctx context.Context, id, at int64, message string) error
	MediaServerPlayedPercent() int64
	RootFolders() []settings.RootFolder
}

// Store is the library side.
type Store interface {
	ResolveFile(ctx context.Context, rootFolderID int64, relPath string) (library.PlaybackHit, bool, error)
	ResolveExternal(ctx context.Context, provider, externalID string) (library.PlaybackHit, bool, error)
	Upsert(ctx context.Context, states []library.PlaybackState) error
	Since(ctx context.Context, serverID int64, serverUserID string) (int64, error)
	SetSince(ctx context.Context, serverID int64, serverUserID string, since int64) error
}

// Client is what a connection's client offers; Lister is nil for a kind that serves only the signed-in user.
type Client struct {
	Reader mediaserver.PlaybackReader
	Lister mediaserver.UserLister
}

// Clients opens the playback side of a connection; a nil Reader says the kind keeps no watch state.
type Clients interface {
	Playback(ctx context.Context, server settings.MediaServer) (Client, error)
}

// Service syncs one connection at a time.
type Service struct {
	Settings Settings
	Store    Store
	Clients  Clients
	Clock    clock.Clock
	Logger   *slog.Logger
}

// Outcome is what one sync did.
type Outcome struct {
	Stored     int
	Unresolved int
}

// providerNames maps a server's provider keys to the names Solarr stores them under.
var providerNames = map[string]string{
	"musicbrainzalbum": "musicbrainz:release", "musicbrainzreleasegroup": "musicbrainz:release-group",
	"musicbrainztrack": "musicbrainz:recording", "musicbrainzrecording": "musicbrainz:recording",
}

// Sync reads every user's plays since the last sync of one connection and stores them; full reads them all again, which
// picks up what an earlier sync could not place because the library did not hold the file yet. The mark of a user moves only
// after all its pages are stored, because a server answers newest first.
func (s Service) Sync(ctx context.Context, serverID int64, full bool) (Outcome, error) {
	servers, err := s.Settings.MediaServers(ctx)
	if err != nil {
		return Outcome{}, err
	}
	var server settings.MediaServer
	for _, one := range servers {
		if one.ID == serverID {
			server = one
		}
	}
	if server.ID == 0 || !server.Enabled || !server.SyncPlayback {
		return Outcome{}, nil
	}
	outcome, err := s.sync(ctx, server, full)
	message := ""
	if err != nil {
		message = "watch state: " + err.Error()
	}
	if err != nil || strings.HasPrefix(server.LastError, "watch state: ") {
		if recorded := s.Settings.SetMediaServerResult(ctx, server.ID, s.Clock.Now().Unix(), message); recorded != nil && err == nil {
			err = recorded
		}
	}
	return outcome, err
}

func (s Service) sync(ctx context.Context, server settings.MediaServer, full bool) (Outcome, error) {
	client, err := s.Clients.Playback(ctx, server)
	if err != nil {
		return Outcome{}, err
	}
	if client.Reader == nil {
		return Outcome{}, fmt.Errorf("playback: %s keeps no watch state to read", server.Kind)
	}
	users, err := s.users(ctx, server, client)
	if err != nil {
		return Outcome{}, err
	}
	stored, err := s.Settings.MediaServerPaths(ctx, server.ID)
	if err != nil {
		return Outcome{}, err
	}
	pairs := make([]mediaserver.PathPair, len(stored))
	for i, one := range stored {
		pairs[i] = mediaserver.PathPair{Solarr: one.SolarrPrefix, Server: one.ServerPrefix}
	}
	var total Outcome
	for _, user := range users {
		done, err := s.syncUser(ctx, server, client.Reader, pairs, user.ServerUserID, full)
		total.Stored += done.Stored
		total.Unresolved += done.Unresolved
		if err != nil {
			return total, fmt.Errorf("user %s: %w", user.Name, err)
		}
	}
	if total.Unresolved > 0 {
		s.logger().Info("played items without a work here", slog.Int64("server_id", server.ID), slog.Int("count", total.Unresolved))
	}
	return total, nil
}

// users refreshes the stored users from the server; new ones start counted and unmapped.
func (s Service) users(ctx context.Context, server settings.MediaServer, client Client) ([]settings.MediaServerUser, error) {
	known, err := s.Settings.MediaServerUsers(ctx, server.ID)
	if err != nil {
		return nil, err
	}
	listed := []mediaserver.User{{ID: server.Username, Name: server.Username}}
	if client.Lister != nil {
		if listed, err = client.Lister.Users(ctx); err != nil {
			return nil, err
		}
	}
	merged := make([]settings.MediaServerUser, 0, len(listed))
	changed := len(known) != len(listed)
	for _, user := range listed {
		entry := settings.MediaServerUser{ServerUserID: user.ID, Name: user.Name, Counted: true}
		found := false
		for _, old := range known {
			if old.ServerUserID == user.ID {
				entry.UserID, entry.Counted, found = old.UserID, old.Counted, true
				changed = changed || old.Name != user.Name
			}
		}
		changed = changed || !found
		merged = append(merged, entry)
	}
	if changed {
		if err := s.Settings.SetMediaServerUsers(ctx, server.ID, merged); err != nil {
			return nil, err
		}
	}
	return merged, nil
}

func (s Service) syncUser(ctx context.Context, server settings.MediaServer, reader mediaserver.PlaybackReader, pairs []mediaserver.PathPair, userID string, full bool) (Outcome, error) {
	since, err := s.Store.Since(ctx, server.ID, userID)
	if err != nil {
		return Outcome{}, err
	}
	if full {
		since = 0
	}
	newest := since
	var outcome Outcome
	for offset := 0; ; offset += PageSize {
		plays, done, err := reader.Played(ctx, userID, since, offset, PageSize)
		if err != nil {
			return outcome, err
		}
		states, unresolved, err := s.resolve(ctx, server.ID, userID, pairs, plays)
		if err != nil {
			return outcome, err
		}
		if err := s.Store.Upsert(ctx, states); err != nil {
			return outcome, err
		}
		outcome.Stored += len(states)
		outcome.Unresolved += unresolved
		for _, play := range plays {
			newest = max(newest, play.LastPlayed)
		}
		if done || len(plays) == 0 {
			break
		}
	}
	if newest > since {
		return outcome, s.Store.SetSince(ctx, server.ID, userID, newest)
	}
	return outcome, nil
}

type stateKey struct{ work, part int64 }

// resolve turns plays into states by file path first, then by provider id; those that match nothing are counted.
func (s Service) resolve(ctx context.Context, serverID int64, userID string, pairs []mediaserver.PathPair, plays []mediaserver.Play) ([]library.PlaybackState, int, error) {
	percent := s.Settings.MediaServerPlayedPercent()
	if percent <= 0 {
		percent = DefaultPlayedPercent
	}
	now := s.Clock.Now().Unix()
	merged := map[stateKey]*library.PlaybackState{}
	var order []stateKey
	unresolved := 0
	for _, play := range plays {
		hit, ok, err := s.find(ctx, pairs, play)
		if err != nil {
			return nil, 0, err
		}
		if !ok {
			unresolved++
			continue
		}
		if play.Whole {
			hit.PartID = 0
		}
		played := play.Played || play.DurationMS > 0 && play.PositionMS*100 >= play.DurationMS*percent
		key := stateKey{hit.WorkID, hit.PartID}
		state, seen := merged[key]
		if !seen {
			state = &library.PlaybackState{ServerID: serverID, ServerUserID: userID, WorkID: hit.WorkID, PartID: hit.PartID, UpdatedAt: now}
			merged[key] = state
			order = append(order, key)
		}
		state.Played = state.Played || played
		state.PlayCount = max(state.PlayCount, play.PlayCount)
		state.PositionMS, state.DurationMS = play.PositionMS, play.DurationMS
		state.LastPlayedAt = max(state.LastPlayedAt, play.LastPlayed)
	}
	states := make([]library.PlaybackState, len(order))
	for i, key := range order {
		states[i] = *merged[key]
	}
	return states, unresolved, nil
}

func (s Service) find(ctx context.Context, pairs []mediaserver.PathPair, play mediaserver.Play) (library.PlaybackHit, bool, error) {
	if play.Path != "" {
		local, mapped := mediaserver.UnmapPath(pairs, play.Path)
		if !mapped {
			local = play.Path
		}
		for _, root := range s.Settings.RootFolders() {
			prefix := strings.TrimRight(root.Path, "/") + "/"
			if rel, ok := strings.CutPrefix(local, prefix); ok {
				hit, found, err := s.Store.ResolveFile(ctx, root.ID, rel)
				if err != nil || found {
					return hit, found, err
				}
			}
		}
	}
	for name, id := range play.ProviderIDs {
		if renamed, ok := providerNames[name]; ok {
			name = renamed
		}
		hit, found, err := s.Store.ResolveExternal(ctx, name, id)
		if err != nil || found {
			return hit, found, err
		}
	}
	return library.PlaybackHit{}, false, nil
}

func (s Service) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}
