package parsetv

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func readJSONL[T any](t *testing.T, name string) []T {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []T
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var row T
		if err := json.Unmarshal(line, &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func TestCorpusQualityClaims(t *testing.T) {
	type row struct {
		Title      string `json:"title"`
		Source     string `json:"source"`
		Resolution int    `json:"resolution"`
		Remux      bool   `json:"remux"`
		Proper     bool   `json:"proper"`
	}
	for _, r := range readJSONL[row](t, "claims_quality") {
		claims := ParseClaims(r.Title)
		if !claimSourceMatches(claims.Source, r.Source) {
			t.Errorf("%q: source %q, want %q", r.Title, claims.Source, r.Source)
		}
		// Sonarr's quality enum implies a resolution; claims only state one the name carries.
		if r.Resolution > 0 && claims.Resolution != 0 && claims.Resolution != r.Resolution {
			t.Errorf("%q: resolution %d, want %d", r.Title, claims.Resolution, r.Resolution)
		}
		if r.Remux && !claims.Remux {
			t.Errorf("%q: remux %v, want %v", r.Title, claims.Remux, r.Remux)
		}
		if (claims.Version > 1) != r.Proper {
			t.Errorf("%q: version %d, proper %v", r.Title, claims.Version, r.Proper)
		}
	}
}

// claimSourceMatches accepts what Sonarr folds into one quality: tv sources for its sdtv and hdtv buckets, rips for bluray.
func claimSourceMatches(got, want string) bool {
	switch want {
	case "sdtv", "hdtv":
		return slices.Contains([]string{"", "sdtv", "hdtv", "pdtv", "dsr", "tvrip"}, got)
	case "bluray":
		return slices.Contains([]string{"bluray", "bdrip", "brrip"}, got)
	}
	return got == want
}

func TestCorpusLanguageClaims(t *testing.T) {
	type row struct {
		Title    string `json:"title"`
		Language string `json:"language"`
	}
	for _, r := range readJSONL[row](t, "claims_language") {
		if got := ParseLanguages(r.Title); !slices.Contains(got, r.Language) {
			t.Errorf("%q: languages %v, want %s among them", r.Title, got, r.Language)
		}
	}
}

func TestClaimsReadRevisionsAndEncoding(t *testing.T) {
	claims := ParseClaims("Show.Name.S01E02.REPACK.2160p.AMZN.WEB-DL.DDP5.1.DV.HEVC-GRP")
	want := Claims{Resolution: 2160, Source: "webdl", VideoCodec: "h265", HDR: "dolby-vision", AudioCodec: "eac3", AudioChannels: "5.1", Service: "AMZN", Version: 2}
	if claims.Resolution != want.Resolution || claims.Source != want.Source || claims.VideoCodec != want.VideoCodec || claims.HDR != want.HDR ||
		claims.AudioCodec != want.AudioCodec || claims.AudioChannels != want.AudioChannels || claims.Service != want.Service || claims.Version != want.Version {
		t.Errorf("got %+v, want %+v", claims, want)
	}
	if got := ParseClaims("[SubsPlease] Show - 01 v2 (1080p) [B1F227CF]").Version; got != 2 {
		t.Errorf("anime v2: version %d, want 2", got)
	}
}

func TestADVDNamesNoLinesButIsStandardDefinition(t *testing.T) {
	claims := ParseClaims("Some.Film.1999.DVDRip.x264-GRP")
	if claims.Source != "dvd" || claims.Resolution != 480 {
		t.Errorf("source %q resolution %d, want dvd at 480", claims.Source, claims.Resolution)
	}
	if hd := ParseClaims("Some.Film.1999.720p.DVDRip.x264-GRP"); hd.Resolution != 720 {
		t.Errorf("a stated resolution wins: %d", hd.Resolution)
	}
}
