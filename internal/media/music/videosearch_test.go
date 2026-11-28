package music_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/acquiring/indexer"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/music"
)

// wantVideo stands up a music video work credited to artist with one wanted item in a music library whose video switch is videosOn.
func (env *searchTestEnv) wantVideo(t *testing.T, artist, title string, year int64, videosOn bool) librarydb.DesiredItem {
	t.Helper()
	var workID int64
	err := env.library.Write(t.Context(), store.PrioBulk, func(ctx context.Context, tx *sql.Tx) error {
		queries := librarydb.New(tx)
		row, err := queries.CreateWork(ctx, librarydb.CreateWorkParams{
			MediaType: "music", CanonicalTitle: title, SortTitle: title, Year: sql.NullInt64{Int64: year, Valid: true},
			Subtype: sql.NullString{String: music.SubtypeMusicVideo, Valid: true},
			AddedAt: env.clock.Now().Unix(), UpdatedAt: env.clock.Now().Unix(),
		})
		workID = row.ID
		return err
	})
	if err != nil {
		t.Fatalf("CreateWork: %v", err)
	}
	env.creditArtist(t, workID, artist)
	profile, err := env.settings.CreateQualityProfile(t.Context(), title+"-profile", alwaysAcceptRules, `{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}`)
	if err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	category, err := env.settings.CreateCategory(t.Context(), "music", title+"-category", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if err := env.settings.SetCategoryMusicVideos(t.Context(), category.ID, videosOn); err != nil {
		t.Fatalf("SetCategoryMusicVideos: %v", err)
	}
	item, err := env.desired.EnsureAlbumItem(t.Context(), library.AlbumRequest{
		WorkID: workID, CategoryID: category.ID, ProfileID: profile.ID, Monitored: true, RequiredWithoutParts: 1,
	})
	if err != nil {
		t.Fatalf("EnsureAlbumItem: %v", err)
	}
	return item
}

func (env *searchTestEnv) videoSearch() music.Search {
	search := env.search()
	search.VideoReader = library.NewSeriesReader(env.library)
	return search
}

// A wanted music video is searched by its own spec: artist and title in the release name, the video categories asked.
func TestSearchItem_AMusicVideoIsFoundByTheNamesOfItsArtistAndTitle(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.wantVideo(t, "Nosi Zada", "Discovery Song", 2019, true)
	searcher := &fakeIndexerSearcher{
		responses: func(indexer.SearchRequest) ([]indexer.RawItem, error) {
			return []indexer.RawItem{
				rawItem("guid-1", "Nosi.Zada-Discovery.Song.2019.1080p.WEB.H264-GROUP"),
				rawItem("guid-2", "Another.Band-Discovery.Song.2019.1080p.WEB.H264-GROUP"),
				rawItem("guid-3", "Nosi.Zada-Other.Song.2019.1080p.WEB.H264-GROUP"),
			}, nil
		},
	}
	result, err := env.videoSearch().SearchItem(t.Context(), item.ID, []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if err != nil {
		t.Fatalf("SearchItem: %v", err)
	}
	if len(result.Decisions) != 1 || result.Decisions[0].Decision.Outcome != "ACCEPT" || result.Decisions[0].ReleaseCandidate.Title != "Nosi.Zada-Discovery.Song.2019.1080p.WEB.H264-GROUP" {
		t.Fatalf("decisions = %+v, want the one release naming the artist and the title, accepted", result.Decisions)
	}
	if len(searcher.requests) == 0 || searcher.requests[0].Query != "Nosi Zada Discovery Song 2019" {
		t.Errorf("first request = %+v, want the artist, title and year as text", searcher.requests)
	}
}

// With the library's switch off a video work is never searched, and nothing is recorded against it.
func TestSearchItem_AMusicVideoIsNotSearchedWhereVideosAreSwitchedOff(t *testing.T) {
	env := newSearchTestEnv(t)
	item := env.wantVideo(t, "Nosi Zada", "Discovery Song", 2019, false)
	searcher := &fakeIndexerSearcher{}
	_, err := env.videoSearch().SearchItem(t.Context(), item.ID, []music.IndexerConn{{ID: 1, Impl: "newznab", Client: searcher}}, env.clock.Now())
	if !errors.Is(err, music.ErrTrackSearchNotSupported) {
		t.Fatalf("SearchItem error = %v, want ErrTrackSearchNotSupported", err)
	}
	if len(searcher.requests) != 0 {
		t.Errorf("%d requests, want none", len(searcher.requests))
	}
}
