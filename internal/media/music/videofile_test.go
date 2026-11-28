package music_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/platform/mediatool"
	"github.com/makearr/Solarr/internal/scanning/scan"
)

func TestInspectVideo(t *testing.T) {
	cases := []struct {
		name string
		path string
		kind string
		tags mediatool.Tags
		long time.Duration
		want music.VideoClaim
	}{
		{"artist - title (year)", "Videos/Katupoken - Sesoge (2019).mkv", "", nil, 4 * time.Minute,
			music.VideoClaim{Artist: "Katupoken", Title: "Sesoge", Year: 2019, Subtype: music.SubtypeMusicVideo}},
		{"folder names the artist", "Katupoken/Live at Pecezinu 2021-05-04 1080p WEB-DL x264.mkv", "", nil, 90 * time.Minute,
			music.VideoClaim{Artist: "Katupoken", Title: "Live at Pecezinu 2021-05-04", Year: 2021, Subtype: music.SubtypeConcertVideo}},
		{"tags win, library kind decides", "x/clip.mp4", "concert_videos", mediatool.Tags{"artist": "Tomuli", "title": "Ki Pece", "date": "2015-03-01"}, 3 * time.Minute,
			music.VideoClaim{Artist: "Tomuli", Title: "Ki Pece", Year: 2015, Subtype: music.SubtypeConcertVideo}},
		{"music kind beats length", "Tomuli/Ki Pece.mp4", "music_videos", nil, 2 * time.Hour,
			music.VideoClaim{Artist: "Tomuli", Title: "Ki Pece", Subtype: music.SubtypeMusicVideo}},
	}
	for _, c := range cases {
		probe := mediatool.Probe{Tags: c.tags, Measured: mediatool.Measured{Duration: c.long}}
		if got := music.InspectVideo(c.path, probe, c.kind); got != c.want {
			t.Errorf("%s: InspectVideo = %+v, want %+v", c.name, got, c.want)
		}
	}
}

func TestIsMusicFile(t *testing.T) {
	for relPath, want := range map[string]bool{
		"a/b.flac": true, "a/b.mkv": true, "a/Extras/b.mkv": false, "a/b.jpg": false, "a/b.nfo": false,
	} {
		if got := music.IsMusicFile(relPath); got != want {
			t.Errorf("IsMusicFile(%q) = %v, want %v", relPath, got, want)
		}
	}
}

func TestObserve_VideoFilesBecomeUnverifiedVideoWorksCreditedToTheirArtist(t *testing.T) {
	opened, fsys, fake := newTestLibrary(t)
	people := library.NewPeople(opened)
	consumer := music.NewConsumer(music.ConsumerConfig{
		FS: fsys, Physical: library.NewPhysical(opened), Identity: library.NewIdentity(opened),
		Unidentified: library.NewUnidentified(opened), People: &people,
		Clock: fake, Logger: slog.New(slog.DiscardHandler),
	})
	content := []byte("video bytes")
	writeMusicFile(t, fsys, "Katupoken/Katupoken - Sesoge (2019).mkv", content)
	probe := observedFile("Katupoken/Katupoken - Sesoge (2019).mkv", int64(len(content)), nil)
	probe.Probe.Measured.Duration = 4 * time.Minute

	root := settings.RootFolder{ID: 1, Path: testRoot, Online: true}
	if err := consumer.Observe(t.Context(), root, 0, []scan.Observed{probe}); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	queries := librarydb.New(opened.Read())
	work, err := queries.GetWorkByExternalID(t.Context(), librarydb.GetWorkByExternalIDParams{
		Provider: music.ProviderLocalVideo, ExternalID: "/media/music/katupoken/katupoken - sesoge (2019)",
	})
	if err != nil {
		t.Fatalf("GetWorkByExternalID: %v", err)
	}
	type got struct {
		Type, Title, Subtype string
		Lead                 bool
	}
	want := got{Type: "music", Title: "Sesoge", Subtype: music.SubtypeMusicVideo, Lead: true}
	have := got{Type: work.MediaType, Title: work.CanonicalTitle, Subtype: work.Subtype.String, Lead: work.LeadPersonID.Valid}
	if have != want {
		t.Errorf("work = %+v, want %+v", have, want)
	}
}
