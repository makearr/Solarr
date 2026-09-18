package library_test

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/platform/clock"
	"github.com/makearr/Solarr/migrations"
)

func newLibrary(t *testing.T) (*store.Store, *librarydb.Queries) {
	t.Helper()
	directory := t.TempDir()
	backups := filepath.Join(directory, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatalf("create backup directory: %v", err)
	}

	fake := clock.NewFakeAtEpoch()
	opened, err := store.Open(t.Context(), fake, store.Config{
		Name: "library", Path: filepath.Join(directory, "library.db"),
		Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })

	owner, err := migrations.Library()
	if err != nil {
		t.Fatalf("migrations: %v", err)
	}
	if err := store.Migrate(t.Context(), opened, backups, owner); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return opened, librarydb.New(opened.Read())
}

func write(t *testing.T, opened *store.Store, mutate func(context.Context, *librarydb.Queries) error) {
	t.Helper()
	err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
		return mutate(ctx, librarydb.New(opened.Read()).WithTx(tx))
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
}

func createWork(t *testing.T, opened *store.Store, mediaType, title string) librarydb.Work {
	t.Helper()
	var created librarydb.Work
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType: mediaType, CanonicalTitle: title, SortTitle: title,
			AddedAt: 1, UpdatedAt: 1,
		})
		created = row
		return err
	})
	return created
}

// ADR-8: the internal id is permanent and a work carries many external ids.
// Resolving one back to its work is the first step of the Identifier chain.
func TestExternalIDResolvesToItsWork(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: "musicbrainz", ExternalID: "rg-123", WorkID: work.ID,
			Confidence: 1, FirstSeenAt: 1,
		})
	})

	found, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz", ExternalID: "rg-123",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if found.ID != work.ID {
		t.Errorf("resolved to work %d, want %d", found.ID, work.ID)
	}
}

// A provider re-pointing an id at another work must move it, not fail or duplicate.
func TestExternalIDCanBeRepointed(t *testing.T) {
	opened, queries := newLibrary(t)
	first := createWork(t, opened, "music", "First")
	second := createWork(t, opened, "music", "Second")

	for _, target := range []int64{first.ID, second.ID} {
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
				Provider: "musicbrainz", ExternalID: "rg-123", WorkID: target,
				Confidence: 1, FirstSeenAt: 1,
			})
		})
	}

	found, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: "musicbrainz", ExternalID: "rg-123",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if found.ID != second.ID {
		t.Errorf("id still points at work %d, want %d", found.ID, second.ID)
	}
}

// ADR-7: an edition carries its own provider identity. A MusicBrainz Release
// MBID identifies the edition while the Release Group MBID identifies the work,
// and identifying from embedded tags depends on both resolving separately.
func TestEditionAndWorkResolveIndependently(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Tender Prey")

	var edition librarydb.Edition
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		created, err := q.CreateEdition(ctx, librarydb.CreateEditionParams{
			WorkID: work.ID, Kind: "remaster", Label: "2010 Remaster",
			IsDefault: 0,
		})
		if err != nil {
			return err
		}
		edition = created
		if err := q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: "musicbrainz", ExternalID: "release-group-1", WorkID: work.ID,
			Confidence: 1, FirstSeenAt: 1,
		}); err != nil {
			return err
		}
		return q.LinkEditionExternalID(ctx, librarydb.LinkEditionExternalIDParams{
			Provider: "musicbrainz", ExternalID: "release-1", EditionID: created.ID,
			Confidence: 1,
		})
	})

	byEdition, err := queries.GetEditionByExternalID(t.Context(),
		librarydb.GetEditionByExternalIDParams{Provider: "musicbrainz", ExternalID: "release-1"})
	if err != nil {
		t.Fatalf("GetEditionByExternalID: %v", err)
	}
	if byEdition.ID != edition.ID || byEdition.WorkID != work.ID {
		t.Errorf("edition %d of work %d, want %d of %d",
			byEdition.ID, byEdition.WorkID, edition.ID, work.ID)
	}

	byWork, err := queries.GetWorkByExternalID(t.Context(),
		librarydb.GetWorkByExternalIDParams{Provider: "musicbrainz", ExternalID: "release-group-1"})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	if byWork.ID != work.ID {
		t.Errorf("release group resolved to %d, want %d", byWork.ID, work.ID)
	}
}

// Invariant 32, and the trap that has already cost a session: a cursor without
// an id tie-break silently skips rows when several share a sort value.
func TestAliasesResolveToTheSameWork(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "tv", "Nathan for You")

	for _, alias := range []string{"nathan for you", "nathan fur dich"} {
		write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
			_, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
				WorkID: work.ID, Title: alias, Normalized: alias, Kind: "alias",
			})
			return err
		})
	}

	for _, alias := range []string{"nathan for you", "nathan fur dich"} {
		found, err := queries.FindWorksByNormalizedTitle(t.Context(),
			librarydb.FindWorksByNormalizedTitleParams{
				Normalized: alias, MediaType: "", PageSize: 10,
			})
		if err != nil {
			t.Fatalf("FindWorksByNormalizedTitle(%q): %v", alias, err)
		}
		if len(found) != 1 || found[0].ID != work.ID {
			t.Errorf("%q resolved to %d works, want just %d", alias, len(found), work.ID)
		}
	}
}

// Deleting a work must take its provider data with it, or the next work to
// reuse an id inherits somebody else's titles.
func TestDeletingAWorkCascades(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if _, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
			WorkID: work.ID, Title: "Cige", Normalized: "dune", Kind: "primary",
		}); err != nil {
			return err
		}
		return q.LinkWorkExternalID(ctx, librarydb.LinkWorkExternalIDParams{
			Provider: "tmdb", ExternalID: "438631", WorkID: work.ID,
			Confidence: 1, FirstSeenAt: 1,
		})
	})

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeleteWork(ctx, work.ID)
	})

	titles, err := queries.ListWorkTitles(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListWorkTitles: %v", err)
	}
	if len(titles) != 0 {
		t.Errorf("%d titles survived the work", len(titles))
	}
	ids, err := queries.ListWorkExternalIDs(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListWorkExternalIDs: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("%d external ids survived the work", len(ids))
	}
}

// ADR-8, Invariant 13: providers disagree, and each keeps its own value so a
// manual override can win without the losing value being destroyed.
func TestFieldsAreStoredPerProvider(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")

	values := map[string]string{"tmdb": "A duke's son", "__manual__": "Mine"}
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for provider, value := range values {
			if err := q.SetWorkField(ctx, librarydb.SetWorkFieldParams{
				WorkID: work.ID, Field: "overview", Provider: provider,
				Value: sql.NullString{String: value, Valid: true}, FetchedAt: 1,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	fields, err := queries.ListWorkFields(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListWorkFields: %v", err)
	}
	if len(fields) != len(values) {
		t.Fatalf("%d values stored, want %d: %+v", len(fields), len(values), fields)
	}
	for _, field := range fields {
		if field.Value.String != values[field.Provider] {
			t.Errorf("%s = %q, want %q", field.Provider, field.Value.String, values[field.Provider])
		}
	}
}

func TestEditionLabelsAreUniquePerWork(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "movie", "Cige")

	add := func() error {
		return opened.Write(t.Context(), store.PrioInteractive,
			func(ctx context.Context, tx *sql.Tx) error {
				q := librarydb.New(opened.Read()).WithTx(tx)
				_, err := q.CreateEdition(ctx, librarydb.CreateEditionParams{
					WorkID: work.ID, Kind: "theatrical", Label: "Theatrical",
				})
				return err
			})
	}
	if err := add(); err != nil {
		t.Fatalf("first edition: %v", err)
	}
	if err := add(); err == nil {
		t.Error("the same edition was accepted twice")
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	opened, _ := newLibrary(t)

	err := opened.Write(t.Context(), store.PrioInteractive,
		func(ctx context.Context, tx *sql.Tx) error {
			q := librarydb.New(opened.Read()).WithTx(tx)
			_, err := q.AddWorkTitle(ctx, librarydb.AddWorkTitleParams{
				WorkID: 999_999, Title: "orphan", Normalized: "orphan", Kind: "primary",
			})
			return err
		})
	if err == nil {
		t.Error("a title was attached to a work that does not exist")
	}
	if !strings.Contains(err.Error(), "FOREIGN KEY") {
		t.Errorf("rejected for the wrong reason: %v", err)
	}
}

// Monitoring on an album type is what Lidarr users actually do ("studio albums,
// not live"), so the classification has to be a value to filter on.
func TestClassificationSelectsWorks(t *testing.T) {
	opened, queries := newLibrary(t)
	studio := createWork(t, opened, "music", "Tender Prey")
	live := createWork(t, opened, "music", "Live Seeds")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, entry := range []struct {
			work  int64
			kind  string
			value string
		}{
			{studio.ID, "primary", "album"},
			{live.ID, "primary", "album"},
			{live.ID, "secondary", "live"},
		} {
			if err := q.SetWorkClassification(ctx, librarydb.SetWorkClassificationParams{
				WorkID: entry.work, Kind: entry.kind, Value: entry.value, Source: "musicbrainz",
			}); err != nil {
				return err
			}
		}
		return nil
	})

	albums, err := queries.FindWorksByClassification(t.Context(),
		librarydb.FindWorksByClassificationParams{
			Kind: "primary", Value: "album", MediaType: "music", PageSize: 10,
		})
	if err != nil {
		t.Fatalf("FindWorksByClassification: %v", err)
	}
	if len(albums) != 2 {
		t.Errorf("%d albums, want 2", len(albums))
	}

	lives, err := queries.FindWorksByClassification(t.Context(),
		librarydb.FindWorksByClassificationParams{
			Kind: "secondary", Value: "live", MediaType: "", PageSize: 10,
		})
	if err != nil {
		t.Fatalf("FindWorksByClassification: %v", err)
	}
	if len(lives) != 1 || lives[0].ID != live.ID {
		t.Errorf("live selection returned %d works, want just %d", len(lives), live.ID)
	}
}

// A work carries one primary classification and many secondary ones, which is
// the shape MusicBrainz and Lidarr both use.
func TestSecondaryClassificationsAccumulate(t *testing.T) {
	opened, queries := newLibrary(t)
	work := createWork(t, opened, "music", "Odds and Sods")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, value := range []string{"compilation", "live"} {
			if err := q.SetWorkClassification(ctx, librarydb.SetWorkClassificationParams{
				WorkID: work.ID, Kind: "secondary", Value: value, Source: "musicbrainz",
			}); err != nil {
				return err
			}
		}
		return nil
	})

	all, err := queries.ListWorkClassifications(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListWorkClassifications: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("%d classifications, want both to survive", len(all))
	}
}

// Galleries are the first content that is neither audio nor video, and they
// share the interned attribute table so quality profiles stay on one path.
func TestImageAttributesInternAlongsideVideo(t *testing.T) {
	opened, queries := newLibrary(t)

	var gallery librarydb.MediaAttribute
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.InternAttributes(ctx, librarydb.InternAttributesParams{
			AttrHash:    "gallery-jpeg-40",
			ImageFormat: sql.NullString{String: "jpeg", Valid: true},
			ColourDepth: sql.NullInt64{Int64: 8, Valid: true},
			PageCount:   sql.NullInt64{Int64: 40, Valid: true},
		})
		gallery = row
		return err
	})

	stored, err := queries.GetAttributes(t.Context(), gallery.ID)
	if err != nil {
		t.Fatalf("GetAttributes: %v", err)
	}
	if stored.ImageFormat.String != "jpeg" || stored.PageCount.Int64 != 40 {
		t.Errorf("image attributes did not round-trip: %+v", stored)
	}
	// The audio and video columns stay null, as the image ones do on a video row.
	if stored.VideoCodec.Valid || stored.AudioCodec.Valid {
		t.Error("a gallery was given audio or video attributes")
	}
}
