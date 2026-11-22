package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// PlaybackState is what one server user has played of a work or one of its parts (PartID 0 is the whole work).
type PlaybackState struct {
	ServerID     int64
	ServerUserID string
	WorkID       int64
	PartID       int64
	Played       bool
	PlayCount    int64
	PositionMS   int64
	DurationMS   int64
	LastPlayedAt int64
	UpdatedAt    int64
}

// PlaybackHit is a work and part a played item resolved to.
type PlaybackHit struct {
	WorkID int64
	PartID int64
}

// Playback keeps watch state read from media servers.
type Playback struct{ store *store.Store }

// NewPlayback wraps an opened library database.
func NewPlayback(opened *store.Store) Playback { return Playback{store: opened} }

// ResolveFile finds the work and part of the library file at a root folder and relative path.
func (p Playback) ResolveFile(ctx context.Context, rootFolderID int64, relPath string) (PlaybackHit, bool, error) {
	row, err := librarydb.New(p.store.Read()).ResolvePlaybackFile(ctx, librarydb.ResolvePlaybackFileParams{RootFolderID: rootFolderID, RelPath: relPath})
	return PlaybackHit(row), found(err), foundErr(err)
}

// ResolveExternal finds a part, else a work, by one provider id.
func (p Playback) ResolveExternal(ctx context.Context, provider, externalID string) (PlaybackHit, bool, error) {
	queries := librarydb.New(p.store.Read())
	part, err := queries.ResolvePlaybackPartByExternalID(ctx, librarydb.ResolvePlaybackPartByExternalIDParams{Provider: provider, ExternalID: externalID})
	if err == nil {
		return PlaybackHit(part), true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return PlaybackHit{}, false, fmt.Errorf("library: resolve %s id: %w", provider, err)
	}
	work, err := queries.ResolvePlaybackWorkByExternalID(ctx, librarydb.ResolvePlaybackWorkByExternalIDParams{Provider: provider, ExternalID: externalID})
	return PlaybackHit{WorkID: work}, found(err), foundErr(err)
}

func found(err error) bool { return err == nil }

func foundErr(err error) error {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return fmt.Errorf("library: resolve played item: %w", err)
}

// Upsert stores the states in chunks of 200; a state replaces the stored one of its key.
func (p Playback) Upsert(ctx context.Context, states []PlaybackState) error {
	for start := 0; start < len(states); start += pendingChunk {
		chunk := states[start:min(start+pendingChunk, len(states))]
		err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
			queries := librarydb.New(tx)
			for _, state := range chunk {
				played := int64(0)
				if state.Played {
					played = 1
				}
				if err := queries.UpsertPlaybackState(ctx, librarydb.UpsertPlaybackStateParams{
					ServerID: state.ServerID, ServerUserID: state.ServerUserID, WorkID: state.WorkID, PartID: state.PartID, Played: played,
					PlayCount: state.PlayCount, PositionMs: state.PositionMS, DurationMs: state.DurationMS,
					LastPlayedAt: sql.NullInt64{Int64: state.LastPlayedAt, Valid: state.LastPlayedAt > 0}, UpdatedAt: state.UpdatedAt,
				}); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("library: store playback: %w", err)
		}
	}
	return nil
}

// Since answers the newest play stored for a server user; zero when none.
func (p Playback) Since(ctx context.Context, serverID int64, serverUserID string) (int64, error) {
	since, err := librarydb.New(p.store.Read()).GetPlaybackSince(ctx, librarydb.GetPlaybackSinceParams{ServerID: serverID, ServerUserID: serverUserID})
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return since, err
}

// SetSince moves a server user's high-water mark.
func (p Playback) SetSince(ctx context.Context, serverID int64, serverUserID string, since int64) error {
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).SetPlaybackSince(ctx, librarydb.SetPlaybackSinceParams{ServerID: serverID, ServerUserID: serverUserID, Since: since})
	})
}

// ClearServer deletes everything stored for a server, in chunks of 200 rows.
func (p Playback) ClearServer(ctx context.Context, serverID int64) error {
	for {
		var removed int64
		err := p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) (err error) {
			removed, err = librarydb.New(tx).DeletePlaybackStateChunk(ctx, serverID)
			return err
		})
		if err != nil {
			return fmt.Errorf("library: clear playback of server %d: %w", serverID, err)
		}
		if removed == 0 {
			break
		}
	}
	return p.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).DeletePlaybackSyncOf(ctx, serverID)
	})
}

// Store is the opened library database, for the readers that sit beside it.
func (p Playback) Store() *store.Store { return p.store }
