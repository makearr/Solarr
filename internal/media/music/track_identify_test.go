package music_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
	"github.com/makearr/Solarr/internal/platform/fsx"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

// fakeTrackLookup answers recording searches by "artist/title" and lookups by id.
type fakeTrackLookup struct {
	searches   map[string][]musicbrainz.RecordingMatch
	recordings map[string]musicbrainz.RecordingMatch
}

func (f fakeTrackLookup) SearchRecordings(_ context.Context, _ musicbrainz.Priority, artist, title string, _ int) ([]musicbrainz.RecordingMatch, error) {
	return f.searches[artist+"/"+title], nil
}

func (f fakeTrackLookup) Recording(_ context.Context, _ musicbrainz.Priority, mbid string) (musicbrainz.RecordingMatch, error) {
	if recording, found := f.recordings[mbid]; found {
		return recording, nil
	}
	return musicbrainz.RecordingMatch{}, musicbrainz.ErrNotFound
}

func newTrackConsumer(t *testing.T, albums music.AlbumLookup, search music.Searcher, tracks music.TrackLookup) (music.Consumer, *store.Store, *fsx.Mem) {
	t.Helper()
	opened, fsys, fake := newTestLibrary(t)
	attempts := library.NewAttempts(opened)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened), Attempts: &attempts, Albums: albums, Search: search, Tracks: tracks,
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	return consumer, opened, fsys
}

func untaggedTrack(t *testing.T, fsys *fsx.Mem, relPath, artist, album, title string, seconds int) scan.Observed {
	t.Helper()
	content := []byte("untagged " + relPath)
	writeMusicFile(t, fsys, relPath, content)
	observed := observedFile(relPath, int64(len(content)), mediatool.Tags{"title": title, "artist": artist, "album": album})
	observed.Probe.Measured.Duration = time.Duration(seconds) * time.Second
	return observed
}

// releaseGroupOf answers which release group a file was identified as, "" when none.
func releaseGroupOf(t *testing.T, opened *store.Store, relPath string) string {
	t.Helper()
	var mbid string
	err := opened.Read().QueryRowContext(t.Context(), `SELECT e.external_id FROM library_path lp
		JOIN work_file f ON f.media_object_id = lp.media_object_id
		JOIN work_external_id e ON e.work_id = f.work_id AND e.provider = ?
		WHERE lp.rel_path = ?`, music.ProviderReleaseGroup, relPath).Scan(&mbid)
	if err != nil {
		return ""
	}
	return mbid
}

func recordingOn(mbid, title string, seconds int, groups ...musicbrainz.ReleaseGroup) musicbrainz.RecordingMatch {
	return musicbrainz.RecordingMatch{RecordingMBID: mbid, Title: title, ArtistCredit: "Lefite", DurationMs: int64(seconds) * 1000, ReleaseGroups: groups}
}

var (
	rgTrue        = musicbrainz.ReleaseGroup{MBID: "rg-true", Title: "True", PrimaryType: "Album", FirstReleaseDate: "2013-09-13"}
	rgWakeSingle  = musicbrainz.ReleaseGroup{MBID: "rg-wake-single", Title: "Budo Me Up", PrimaryType: "Single", FirstReleaseDate: "2013-06-17"}
	rgBravo       = musicbrainz.ReleaseGroup{MBID: "rg-bravo", Title: "Bravo Hits 82", PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}, FirstReleaseDate: "2013-08-01"}
	rgStories     = musicbrainz.ReleaseGroup{MBID: "rg-stories", Title: "Stories", PrimaryType: "Album", FirstReleaseDate: "2015-10-02"}
	rgLevels      = musicbrainz.ReleaseGroup{MBID: "rg-levels", Title: "Levels", PrimaryType: "Single", FirstReleaseDate: "2011-10-28"}
	rgForever     = musicbrainz.ReleaseGroup{MBID: "rg-forever", Title: "Lefite Bacizil", PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}, FirstReleaseDate: "2020"}
	rgEssentials  = musicbrainz.ReleaseGroup{MBID: "rg-essentials", Title: "Essentials", PrimaryType: "Album", SecondaryTypes: []string{"Compilation"}, FirstReleaseDate: "2018"}
	rgLookingGlas = musicbrainz.ReleaseGroup{MBID: "rg-looking-glass", Title: "Besag Gekulup the Bemokav Rifal", PrimaryType: "Album", SecondaryTypes: []string{"Soundtrack"}, FirstReleaseDate: "2016-05-27"}
)

// A playlist is no album: each of its tracks goes to the album its recording comes from, the
// studio album before the single before a compilation ("Essentials", the main library).
func TestObserve_APlaylistFolderIsIdentifiedTrackByTrack(t *testing.T) {
	tracks := fakeTrackLookup{searches: map[string][]musicbrainz.RecordingMatch{
		"Lefite/Budo Me Up":  {recordingOn("rec-wake", "Budo Me Up", 247, rgWakeSingle, rgBravo, rgTrue)},
		"Lefite/Levels":      {recordingOn("rec-levels", "Levels", 200, rgForever, rgLevels)},
		"Lefite/Caz Gotabam": {recordingOn("rec-brother", "Caz Gotabam", 255, rgStories, rgTrue)},
	}}
	consumer, opened, fsys := newTrackConsumer(t, &fakeAlbumLookup{}, nil, tracks)
	batch := []scan.Observed{
		untaggedTrack(t, fsys, "Lefite/Playlist (2018)/01.m4a", "Lefite", "Playlist", "Budo Me Up", 248),
		untaggedTrack(t, fsys, "Lefite/Playlist (2018)/02.m4a", "Lefite", "Playlist", "Levels", 201),
		untaggedTrack(t, fsys, "Lefite/Playlist (2018)/03.m4a", "Lefite", "Playlist", "Caz Gotabam", 255),
	}

	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: testRoot, Online: true}, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	for relPath, want := range map[string]string{
		"Lefite/Playlist (2018)/01.m4a": "rg-true", "Lefite/Playlist (2018)/02.m4a": "rg-levels", "Lefite/Playlist (2018)/03.m4a": "rg-true",
	} {
		if got := releaseGroupOf(t, opened, relPath); got != want {
			t.Errorf("%s identified as %q, want %q", relPath, got, want)
		}
	}
}

// The folder is a hint: a recording that is on the album the folder names goes there, even a compilation.
func TestObserve_ATrackGoesToTheAlbumItsFolderNamesWhenItIsOnIt(t *testing.T) {
	tracks := fakeTrackLookup{searches: map[string][]musicbrainz.RecordingMatch{
		"Lefite/Budo Me Up": {recordingOn("rec-wake", "Budo Me Up", 247, rgTrue, rgEssentials)},
	}}
	consumer, opened, fsys := newTrackConsumer(t, &fakeAlbumLookup{}, nil, tracks)
	batch := []scan.Observed{untaggedTrack(t, fsys, "Lefite/Essentials (2018)/01.m4a", "Lefite", "Essentials", "Budo Me Up", 247)}

	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: testRoot, Online: true}, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got := releaseGroupOf(t, opened, "Lefite/Essentials (2018)/01.m4a"); got != "rg-essentials" {
		t.Errorf("identified as %q, want the folder's own album rg-essentials", got)
	}
}

// A file sorted into the wrong album folder keeps no track on that album: it goes to the album its
// recording is on, never joined to its folder's by the siblings (Lidarr misfiles, the main library).
func TestObserve_AMisfiledTrackGoesToItsOwnAlbumNotItsFolders(t *testing.T) {
	tracks := fakeTrackLookup{searches: map[string][]musicbrainz.RecordingMatch{
		"P!po/Just Like Tepa": {{RecordingMBID: "rec-fire", Title: "Just Like Tepa", ArtistCredit: "P!po", DurationMs: 215000,
			ReleaseGroups: []musicbrainz.ReleaseGroup{rgLookingGlas}}},
	}}
	// Its tags still name the folder's album, which a title search alone would answer.
	search := &fakeSearcher{matches: []musicbrainz.ReleaseGroupMatch{
		{ReleaseGroup: musicbrainz.ReleaseGroup{MBID: releaseGroupMBID, Title: "Kebilegev Bibihi"}, ArtistCredit: "P!po"},
	}}
	consumer, opened, fsys := newTrackConsumer(t, traumaLookup(releaseGroupMBID), search, tracks)
	misfiled := "P!po/Kebilegev Bibihi (2017)/P!po - 07 - Just Like Tepa.m4a"
	batch := append(untaggedTraumaBatch(t, fsys),
		untaggedTrack(t, fsys, misfiled, "P!po", "Kebilegev Bibihi", "Just Like Tepa", 215))

	if err := consumer.Observe(t.Context(), settings.RootFolder{ID: 1, Path: testRoot, Online: true}, 0, batch); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if got := releaseGroupOf(t, opened, "P!po/Kebilegev Bibihi (2017)/P!po - 01 - Kebilegev Bibihi.m4a"); got != releaseGroupMBID {
		t.Fatalf("the album's own files went to %q, want %q", got, releaseGroupMBID)
	}
	if got := releaseGroupOf(t, opened, misfiled); got != "rg-looking-glass" {
		t.Errorf("misfiled track identified as %q, want the album its recording is on", got)
	}
}
