package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// BlockReason says what was wrong with the content (blocked_content.reason,
// migration 0019).
type BlockReason string

// The five reasons blocked_content.reason accepts.
const (
	BlockReasonFake        BlockReason = "FAKE"
	BlockReasonMislabelled BlockReason = "MISLABELLED"
	BlockReasonCorrupt     BlockReason = "CORRUPT"
	BlockReasonUnwanted    BlockReason = "UNWANTED"
	BlockReasonExecutable  BlockReason = "EXECUTABLE"
)

// BlockSource says who created the block (blocked_content.source).
type BlockSource string

// The two sources blocked_content.source accepts.
const (
	BlockSourceUser BlockSource = "user"
	BlockSourceAuto BlockSource = "auto"
)

// ErrNoHash reports a block with no identifier at all -- the same guard the
// table's own CHECK constraint enforces, raised here first so the error
// names the actual problem rather than a generic constraint failure.
var ErrNoHash = errors.New("blocked_content: at least one of oshash, quick_hash, chromaprint or phash is required")

// BlockedContent is one blocked_content row -- bytes that never enter the
// library again (ADR-53 part 2), keyed on content hashes, never on a
// filename (Invariant 82).
type BlockedContent struct {
	ID           int64
	OSHash       string
	QuickHash    string
	Chromaprint  string
	Phash        string
	SizeBytes    int64
	Reason       BlockReason
	Note         string
	MediaType    string
	Title        string
	ReleaseGroup string
	Source       BlockSource
	UserID       *int64
	ExpiresAt    *int64
	CreatedAt    int64
	HitCount     int64
	LastHitAt    *int64
}

// NewBlock is what Create needs. At least one of OSHash/QuickHash/
// Chromaprint/Phash must be set (ErrNoHash otherwise). ExpiresAt nil means
// permanent -- manual blocks always are (section 10.8); only an automatic
// entry should ever set one.
type NewBlock struct {
	OSHash       string
	QuickHash    string
	Chromaprint  string
	Phash        string
	SizeBytes    int64
	Reason       BlockReason
	Note         string
	MediaType    string
	Title        string
	ReleaseGroup string
	Source       BlockSource
	UserID       *int64
	ExpiresAt    *int64
	CreatedAt    int64
}

// BlockedContentStore is ADR-53 part 2's own repository, over one open
// library store.
type BlockedContentStore struct{ store *store.Store }

// NewBlockedContentStore builds it over an open library store.
func NewBlockedContentStore(opened *store.Store) BlockedContentStore {
	return BlockedContentStore{store: opened}
}

// Create records a new block. A caller checking for existing library matches
// first (section 10.8's own "warns when the hash already matches files in
// the library" guard) is the compound remove-and-block action's job, not
// this method's -- Create only ever records what it is told to.
func (b BlockedContentStore) Create(ctx context.Context, req NewBlock) (BlockedContent, error) {
	if req.OSHash == "" && req.QuickHash == "" && req.Chromaprint == "" && req.Phash == "" {
		return BlockedContent{}, ErrNoHash
	}

	var row librarydb.BlockedContent
	err := b.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		var writeErr error
		row, writeErr = librarydb.New(tx).CreateBlockedContent(ctx, librarydb.CreateBlockedContentParams{
			Oshash: nullString(req.OSHash), QuickHash: nullString(req.QuickHash),
			Chromaprint: nullString(req.Chromaprint), Phash: nullString(req.Phash),
			SizeBytes: req.SizeBytes, Reason: string(req.Reason), Note: nullString(req.Note),
			MediaType: nullString(req.MediaType), Title: req.Title, ReleaseGroup: req.ReleaseGroup,
			Source: string(req.Source), UserID: nullInt64(req.UserID),
			ExpiresAt: nullInt64(req.ExpiresAt), CreatedAt: req.CreatedAt,
		})
		return writeErr
	})
	if err != nil {
		return BlockedContent{}, fmt.Errorf("blocked_content: create: %w", err)
	}
	return blockedContentFromRow(row), nil
}

// Get answers one block by id. A missing id surfaces sql.ErrNoRows, wrapped.
func (b BlockedContentStore) Get(ctx context.Context, id int64) (BlockedContent, error) {
	row, err := librarydb.New(b.store.Read()).GetBlockedContent(ctx, id)
	if err != nil {
		return BlockedContent{}, fmt.Errorf("blocked_content: get %d: %w", id, err)
	}
	return blockedContentFromRow(row), nil
}

// FindByHash is the import gate's own check (section 10.3 step 3): every
// unexpired block matching either exact hash. Empty strings match nothing --
// a caller with no oshash yet (not probed) passes quick_hash alone rather
// than an empty string that would otherwise compare true against a NULL
// column via IS NOT NULL's own guard.
func (b BlockedContentStore) FindByHash(ctx context.Context, oshash, quickHash string, now int64) ([]BlockedContent, error) {
	rows, err := librarydb.New(b.store.Read()).FindBlockedContentByExactHash(ctx, librarydb.FindBlockedContentByExactHashParams{
		Now: sql.NullInt64{Int64: now, Valid: true}, Oshash: nullString(oshash), QuickHash: nullString(quickHash),
	})
	if err != nil {
		return nil, fmt.Errorf("blocked_content: find by hash: %w", err)
	}
	blocks := make([]BlockedContent, len(rows))
	for i, row := range rows {
		blocks[i] = blockedContentFromRow(row)
	}
	return blocks, nil
}

// List pages through every block, oldest first.
func (b BlockedContentStore) List(ctx context.Context, afterID int64, pageSize int64) ([]BlockedContent, error) {
	rows, err := librarydb.New(b.store.Read()).ListBlockedContent(ctx, librarydb.ListBlockedContentParams{
		AfterID: afterID, PageSize: pageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("blocked_content: list: %w", err)
	}
	blocks := make([]BlockedContent, len(rows))
	for i, row := range rows {
		blocks[i] = blockedContentFromRow(row)
	}
	return blocks, nil
}

// RecordHit marks the block as having fired again -- shown wherever a block
// is displayed (architecture.md's own "how many times it has fired since").
func (b BlockedContentStore) RecordHit(ctx context.Context, id, at int64) error {
	err := b.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RecordBlockedContentHit(ctx, librarydb.RecordBlockedContentHitParams{
			LastHitAt: sql.NullInt64{Int64: at, Valid: true}, ID: id,
		})
	})
	if err != nil {
		return fmt.Errorf("blocked_content: record hit on %d: %w", id, err)
	}
	return nil
}

// Remove deletes a block outright -- reversing one, not the same as an entry
// expiring on its own.
func (b BlockedContentStore) Remove(ctx context.Context, id int64) error {
	err := b.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).RemoveBlockedContent(ctx, id)
	})
	if err != nil {
		return fmt.Errorf("blocked_content: remove %d: %w", id, err)
	}
	return nil
}

// PurgeExpired removes every block whose expiry has passed -- manual blocks
// never expire (section 10.8), so this only ever touches automatic entries,
// matching section 10.11's identical rule for blocklisted_release.
func (b BlockedContentStore) PurgeExpired(ctx context.Context, now int64) error {
	err := b.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		return librarydb.New(tx).PurgeExpiredBlockedContent(ctx, sql.NullInt64{Int64: now, Valid: true})
	})
	if err != nil {
		return fmt.Errorf("blocked_content: purge expired: %w", err)
	}
	return nil
}

func blockedContentFromRow(row librarydb.BlockedContent) BlockedContent {
	return BlockedContent{
		ID: row.ID, OSHash: row.Oshash.String, QuickHash: row.QuickHash.String,
		Chromaprint: row.Chromaprint.String, Phash: row.Phash.String, SizeBytes: row.SizeBytes,
		Reason: BlockReason(row.Reason), Note: row.Note.String, MediaType: row.MediaType.String,
		Title: row.Title, ReleaseGroup: row.ReleaseGroup, Source: BlockSource(row.Source),
		UserID: int64Ptr(row.UserID), ExpiresAt: int64Ptr(row.ExpiresAt), CreatedAt: row.CreatedAt,
		HitCount: row.HitCount, LastHitAt: int64Ptr(row.LastHitAt),
	}
}
