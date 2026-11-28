package music_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/media/music"
	"github.com/makearr/Solarr/internal/metadata/musicbrainz"
)

type fakeVideoLookup struct {
	recordings []musicbrainz.RecordingMatch
	groups     []musicbrainz.ReleaseGroupMatch
}

func (f fakeVideoLookup) SearchVideoRecordings(context.Context, musicbrainz.Priority, string, string, int) ([]musicbrainz.RecordingMatch, error) {
	return f.recordings, nil
}

func (f fakeVideoLookup) Search(context.Context, musicbrainz.Priority, string, string, int) ([]musicbrainz.ReleaseGroupMatch, error) {
	return f.groups, nil
}

func TestIdentifyVideo(t *testing.T) {
	clip := musicbrainz.RecordingMatch{
		RecordingMBID: "rec-sesoge", Title: "Sesoge", ArtistCredit: "Katupoken",
		ReleaseGroups: []musicbrainz.ReleaseGroup{
			{MBID: "rg-best-of", Title: "Best of", FirstReleaseDate: "2015"},
			{MBID: "rg-sesoge", Title: "Sesoge Songs", FirstReleaseDate: "2011-10-28"},
		},
	}
	live := musicbrainz.ReleaseGroupMatch{
		ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-live", Title: "Live at the Plaza", SecondaryTypes: []string{"Live"}, FirstReleaseDate: "2014-05-01"},
		ArtistCredit: "Katupoken",
	}
	studio := musicbrainz.ReleaseGroupMatch{
		ReleaseGroup: musicbrainz.ReleaseGroup{MBID: "rg-studio", Title: "Live at the Plaza", FirstReleaseDate: "2014"}, ArtistCredit: "Katupoken",
	}
	for name, tc := range map[string]struct {
		lookup fakeVideoLookup
		claim  music.VideoClaim
		want   *music.VideoIdentity
	}{
		"a music video is its recording and the earliest album of its track": {
			fakeVideoLookup{recordings: []musicbrainz.RecordingMatch{clip}},
			music.VideoClaim{Artist: "Katupoken", Title: "Sesoge", Subtype: music.SubtypeMusicVideo},
			&music.VideoIdentity{Provider: music.ProviderVideoRecording, ExternalID: "rec-sesoge", Title: "Sesoge", Year: 2011, ReleaseGroupMBID: "rg-sesoge", Confidence: 1},
		},
		"another artist's recording is not it": {
			fakeVideoLookup{recordings: []musicbrainz.RecordingMatch{{RecordingMBID: "rec-x", Title: "Sesoge", ArtistCredit: "Zumepa"}}},
			music.VideoClaim{Artist: "Katupoken", Title: "Sesoge", Subtype: music.SubtypeMusicVideo}, nil,
		},
		"two recordings equally good stay for a person": {
			fakeVideoLookup{recordings: []musicbrainz.RecordingMatch{clip, {RecordingMBID: "rec-2", Title: "Sesoge", ArtistCredit: "Katupoken"}}},
			music.VideoClaim{Artist: "Katupoken", Title: "Sesoge", Subtype: music.SubtypeMusicVideo}, nil,
		},
		"no artist named is never committed": {
			fakeVideoLookup{recordings: []musicbrainz.RecordingMatch{clip}},
			music.VideoClaim{Title: "Sesoge", Subtype: music.SubtypeMusicVideo}, nil,
		},
		"a concert is the live release group it films": {
			fakeVideoLookup{groups: []musicbrainz.ReleaseGroupMatch{studio, live}},
			music.VideoClaim{Artist: "Katupoken", Title: "Live at the Plaza", Year: 2014, Subtype: music.SubtypeConcertVideo},
			&music.VideoIdentity{Provider: music.ProviderLiveVideo, ExternalID: "rg-live", Title: "Live at the Plaza", Year: 2014, ReleaseGroupMBID: "rg-live", Confidence: 1},
		},
		"a concert of another year than the live album falls under the threshold": {
			fakeVideoLookup{groups: []musicbrainz.ReleaseGroupMatch{live}},
			music.VideoClaim{Artist: "Katupoken", Title: "Live at the Plaza", Year: 2003, Subtype: music.SubtypeConcertVideo}, nil,
		},
	} {
		got, err := music.IdentifyVideo(t.Context(), tc.lookup, tc.claim, 0.9)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %+v, want %+v", name, got, tc.want)
		}
	}
}
