package library

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

// AttributeClaim mirrors media_attributes' full column set (ADR-39: "release
// name = claims, ffprobe = facts. Both stored"). Both halves intern through the
// same shape, since they are the same table -- a caller sets only the fields it
// actually knows about; everything else stays zero, which Intern stores as NULL
// rather than a false claim of "known and empty".
type AttributeClaim struct {
	Resolution, Source, VideoCodec, HDR string
	AudioCodec, AudioChannels           string
	BitDepth                            int
	Languages, Subtitles, EditionTokens []string
	ReleaseGroup                        string
	ProperRepack                        bool
	FrameRate                           float64
	ScanType, Container                 string
	Lossless                            bool
	SampleRate                          int
	ImageFormat                         string
	ColourDepth, PageCount              int
	BitrateKbps                         int
	BitrateMode, VBRPreset              string
}

// Hash is media_attributes.attr_hash: a deterministic digest of every field in
// a fixed order, so two files claiming or measuring an identical set intern to
// the same row (section 5.2's interning, proven at the 500k fixture by keeping
// distinct rows in the thousands). Field order must never change once any row
// exists, or every existing hash stops matching what re-deriving it produces.
func (c AttributeClaim) Hash() string {
	sum := sha256.New()
	fields := []string{
		c.Resolution, c.Source, c.VideoCodec, c.HDR, c.AudioCodec, c.AudioChannels,
		strconv.Itoa(c.BitDepth), strings.Join(c.Languages, ","), strings.Join(c.Subtitles, ","),
		strings.Join(c.EditionTokens, ","), c.ReleaseGroup, strconv.FormatBool(c.ProperRepack),
		strconv.FormatFloat(c.FrameRate, 'f', -1, 64), c.ScanType, c.Container,
		strconv.FormatBool(c.Lossless), strconv.Itoa(c.SampleRate), c.ImageFormat,
		strconv.Itoa(c.ColourDepth), strconv.Itoa(c.PageCount), strconv.Itoa(c.BitrateKbps),
		c.BitrateMode, c.VBRPreset,
	}
	for _, field := range fields {
		sum.Write([]byte(field))
		sum.Write([]byte{0})
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Attributes interns media_attributes rows for both halves of ADR-39.
type Attributes struct{ store *store.Store }

// NewAttributes builds it over an open library store.
func NewAttributes(opened *store.Store) Attributes { return Attributes{store: opened} }

// Intern finds or creates the media_attributes row for claim, returning its
// id. This must be the only path that ever writes one, or two equivalent
// claims could land in different rows and defeat interning.
func (a Attributes) Intern(ctx context.Context, claim AttributeClaim) (int64, error) {
	var id int64
	err := a.store.Write(ctx, store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		row, err := librarydb.New(tx).InternAttributes(ctx, internParams(claim))
		if err != nil {
			return err
		}
		id = row.ID
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("library: intern attributes: %w", err)
	}
	return id, nil
}

func internParams(c AttributeClaim) librarydb.InternAttributesParams {
	return librarydb.InternAttributesParams{
		AttrHash: c.Hash(), Resolution: nullString(c.Resolution), Source: nullString(c.Source),
		VideoCodec: nullString(c.VideoCodec), Hdr: nullString(c.HDR),
		AudioCodec: nullString(c.AudioCodec), AudioChannels: nullString(c.AudioChannels),
		BitDepth:  sql.NullInt64{Int64: int64(c.BitDepth), Valid: c.BitDepth > 0},
		Languages: jsonArray(c.Languages), Subtitles: jsonArray(c.Subtitles),
		EditionTokens: jsonArray(c.EditionTokens), ReleaseGroup: nullString(c.ReleaseGroup),
		ProperRepack: boolToInt(c.ProperRepack),
		FrameRate:    sql.NullFloat64{Float64: c.FrameRate, Valid: c.FrameRate > 0},
		ScanType:     nullString(c.ScanType), Container: nullString(c.Container),
		Lossless:    sql.NullInt64{Int64: boolToInt(c.Lossless), Valid: c.AudioCodec != ""},
		SampleRate:  sql.NullInt64{Int64: int64(c.SampleRate), Valid: c.SampleRate > 0},
		ImageFormat: nullString(c.ImageFormat),
		ColourDepth: sql.NullInt64{Int64: int64(c.ColourDepth), Valid: c.ColourDepth > 0},
		PageCount:   sql.NullInt64{Int64: int64(c.PageCount), Valid: c.PageCount > 0},
		BitrateKbps: sql.NullInt64{Int64: int64(c.BitrateKbps), Valid: c.BitrateKbps > 0},
		BitrateMode: nullString(c.BitrateMode), VbrPreset: nullString(c.VBRPreset),
	}
}

// attributeClaimFromRow reverses internParams: an interned media_attributes
// row back into the same Go shape a caller built it from, so a claimed row
// looked up by id can feed CompareAttributes exactly like the value that was
// interned to begin with.
func attributeClaimFromRow(row librarydb.MediaAttribute) AttributeClaim {
	return AttributeClaim{
		Resolution: row.Resolution.String, Source: row.Source.String,
		VideoCodec: row.VideoCodec.String, HDR: row.Hdr.String,
		AudioCodec: row.AudioCodec.String, AudioChannels: row.AudioChannels.String,
		BitDepth: int(row.BitDepth.Int64), Languages: jsonStrings(row.Languages),
		Subtitles: jsonStrings(row.Subtitles), EditionTokens: jsonStrings(row.EditionTokens),
		ReleaseGroup: row.ReleaseGroup.String, ProperRepack: row.ProperRepack != 0,
		FrameRate: row.FrameRate.Float64, ScanType: row.ScanType.String,
		Container: row.Container.String, Lossless: row.Lossless.Int64 != 0,
		SampleRate: int(row.SampleRate.Int64), ImageFormat: row.ImageFormat.String,
		ColourDepth: int(row.ColourDepth.Int64), PageCount: int(row.PageCount.Int64),
		BitrateKbps: int(row.BitrateKbps.Int64), BitrateMode: row.BitrateMode.String,
		VBRPreset: row.VbrPreset.String,
	}
}

func jsonStrings(column sql.NullString) []string {
	if !column.Valid || column.String == "" {
		return nil
	}
	var values []string
	if err := json.Unmarshal([]byte(column.String), &values); err != nil {
		return nil
	}
	return values
}

// jsonArray encodes a string slice the way media_attributes' JSON-array columns
// expect it, NULL for an empty slice rather than the literal string "[]" --
// GetAttributes' own callers already treat an empty column as "none stated".
func jsonArray(values []string) sql.NullString {
	if len(values) == 0 {
		return sql.NullString{}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(encoded), Valid: true}
}
