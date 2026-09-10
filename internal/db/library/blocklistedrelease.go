package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/MediaNexus/internal/db/store"
	"github.com/makearr/MediaNexus/internal/db/store/gen/librarydb"
)

// BlocklistReason says why a release must not be re-grabbed
// (blocklisted_release.reason, section 10.11).
type BlocklistReason string

// The six reasons blocklisted_release.reason accepts.
const (
	BlocklistDownloadFailed BlocklistReason = "DOWNLOAD_FAILED"
	BlocklistStalled        BlocklistReason = "STALLED"
	BlocklistCorrupt        BlocklistReason = "CORRUPT"
	BlocklistImportFailed   BlocklistReason = "IMPORT_FAILED"
	BlocklistRejectedWorse  BlocklistReason = "REJECTED_WORSE"
	// BlocklistManual is what ADR-53's own "remove and block" action uses --
	// FAKE is recorded on blocked_content instead; this row just stops the
	// same release from being re-grabbed.
	BlocklistManual BlocklistReason = "MANUAL"
)

// BlocklistSource says who created the entry -- an automatic one is a fact
// about the download, never a judgement (section 10.11); only a user source
// is ever eligible to feed ADR-45 reputation, and even then only through
// blocked_content's own reason=FAKE, not through this table at all.
type BlocklistSource string

// The two sources blocklisted_release.source accepts.
const (
	BlocklistSourceAuto BlocklistSource = "auto"
	BlocklistSourceUser BlocklistSource = "user"
)

// ErrNoBlocklistKey reports an entry with neither a guid nor a content hash
// -- the same guard the table's own CHECK constraint enforces.
var ErrNoBlocklistKey = errors.New("blocklisted_release: guid or content_hash is required")

// BlocklistEntry is one blocklisted_release row: a release that must not be
// re-grabbed, matched by indexer guid or by content hash (section 10.11).
type BlocklistEntry struct {
	ID            int64
	IndexerID     *int64
	GUID          string
	ContentHash   string
	Title         string
	DesiredItemID *int64
	Reason        BlocklistReason
	DetailJSON    string
	Source        BlocklistSource
	ExpiresAt     *int64
	CreatedAt     int64
}

// NewBlocklistEntry is what Create needs. ExpiresAt nil means permanent --
// only an automatic entry should ever set one (section 10.11).
type NewBlocklistEntry struct {
	IndexerID     *int64
	GUID          string
	ContentHash   string
	Title         string
	DesiredItemID *int64
	Reason        BlocklistReason
	DetailJSON    string
	Source        BlocklistSource
	ExpiresAt     *int64
	CreatedAt     int64
}

// BlocklistStore is section 10.11's own repository, over one open library
// store.
type BlocklistStore struct{ store *store.Store }

// NewBlocklistStore builds it over an open library store.
func NewBlocklistStore(opened *store.Store) BlocklistStore { return BlocklistStore{store: opened} }

// Create records a blocklist entry.
func (b BlocklistStore) Create(ctx context.Context, req NewBlocklistEntry) (BlocklistEntry, error) {
	if req.GUID == "" && req.ContentHash == "" {
		return BlocklistEntry{}, ErrNoBlocklistKey
	}

	var row librarydb.BlocklistedRelease
	err := b.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		row, writeErr = librarydb.New(tx).BlocklistRelease(ctx, librarydb.BlocklistReleaseParams{
			IndexerID: nullInt64(req.IndexerID), Guid: nullString(req.GUID),
			ContentHash: nullString(req.ContentHash), Title: req.Title,
			DesiredItemID: nullInt64(req.DesiredItemID), Reason: string(req.Reason),
			DetailJson: nullString(req.DetailJSON), Source: string(req.Source),
			ExpiresAt: nullInt64(req.ExpiresAt), CreatedAt: req.CreatedAt,
		})
		return writeErr
	})
	if err != nil {
		return BlocklistEntry{}, fmt.Errorf("blocklisted_release: create: %w", err)
	}
	return blocklistEntryFromRow(row), nil
}

// IsBlocked is the pipeline's own check (section 10.11), between dedupe and
// evaluate: matched by indexer guid or by content hash, unexpired only.
func (b BlocklistStore) IsBlocked(ctx context.Context, indexerID *int64, guid, contentHash string, now int64) (bool, error) {
	blocked, err := librarydb.New(b.store.Read()).IsBlocklisted(ctx, librarydb.IsBlocklistedParams{
		Now: sql.NullInt64{Int64: now, Valid: true}, IndexerID: nullInt64(indexerID),
		Guid: nullString(guid), ContentHash: nullString(contentHash),
	})
	if err != nil {
		return false, fmt.Errorf("blocklisted_release: is blocked: %w", err)
	}
	return blocked > 0, nil
}

// List pages through every entry, oldest first.
func (b BlocklistStore) List(ctx context.Context, afterID, pageSize int64) ([]BlocklistEntry, error) {
	rows, err := librarydb.New(b.store.Read()).ListBlocklist(ctx, librarydb.ListBlocklistParams{
		AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("blocklisted_release: list: %w", err)
	}
	entries := make([]BlocklistEntry, len(rows))
	for i, row := range rows {
		entries[i] = blocklistEntryFromRow(row)
	}
	return entries, nil
}

// Remove deletes an entry outright.
func (b BlocklistStore) Remove(ctx context.Context, id int64) error {
	err := b.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RemoveFromBlocklist(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("blocklisted_release: remove %d: %w", id, err)
	}
	return nil
}

// PurgeExpired removes every entry whose expiry has passed -- auto entries
// may expire because indexers repair things; manual ones never do.
func (b BlocklistStore) PurgeExpired(ctx context.Context, now int64) error {
	err := b.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).PurgeExpiredBlocklist(ctx, sql.NullInt64{Int64: now, Valid: true})
	})
	if err != nil {
		return fmt.Errorf("blocklisted_release: purge expired: %w", err)
	}
	return nil
}

func blocklistEntryFromRow(row librarydb.BlocklistedRelease) BlocklistEntry {
	return BlocklistEntry{
		ID: row.ID, IndexerID: int64Ptr(row.IndexerID), GUID: row.Guid.String,
		ContentHash: row.ContentHash.String, Title: row.Title, DesiredItemID: int64Ptr(row.DesiredItemID),
		Reason: BlocklistReason(row.Reason), DetailJSON: row.DetailJson.String,
		Source: BlocklistSource(row.Source), ExpiresAt: int64Ptr(row.ExpiresAt), CreatedAt: row.CreatedAt,
	}
}
