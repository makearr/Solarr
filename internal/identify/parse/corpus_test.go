package parse_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/identify/parse"
)

// corpusCase is one line of testdata/releases/*.jsonl -- ADR-18's golden corpus.
// Every mis-parse found becomes a case here before it is fixed, growing toward
// the architecture's 10,000+ target; this seed file starts that growth with real
// release names (collecting them is safe under any licence -- ADR-18's own text).
type corpusCase struct {
	Input          string  `json:"input"`
	Artist         string  `json:"artist"`
	Album          string  `json:"album"`
	Year           int     `json:"year"`
	YearEnd        int     `json:"year_end"`
	Format         string  `json:"format"`
	Lossless       bool    `json:"lossless"`
	Container      string  `json:"container"`
	BitrateKbps    int     `json:"bitrate_kbps"`
	Source         string  `json:"source"`
	Edition        string  `json:"edition"`
	ReleaseGroup   string  `json:"release_group"`
	Discs          int     `json:"discs"`
	VariousArtists bool    `json:"various_artists"`
	Discography    bool    `json:"discography"`
	MinConfidence  float64 `json:"min_confidence"`
	// MaxConfidence is optional (zero means "no ceiling checked"): used for the
	// no-artist fixtures to prove ADR-18's "report confidence, never guess
	// silently" actually holds, not just that confidence clears some floor.
	MaxConfidence float64 `json:"max_confidence"`
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()
	matches, err := filepath.Glob("testdata/releases/*.jsonl")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no corpus files found under testdata/releases -- the harness has nothing to prove itself against")
	}

	var cases []corpusCase
	for _, path := range matches {
		file, err := os.Open(path)
		if err != nil {
			t.Fatalf("open %s: %v", path, err)
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for lineNumber := 1; scanner.Scan(); lineNumber++ {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var one corpusCase
			if err := json.Unmarshal([]byte(line), &one); err != nil {
				t.Fatalf("%s:%d: %v", path, lineNumber, err)
			}
			cases = append(cases, one)
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan %s: %v", path, err)
		}
		_ = file.Close()
	}
	return cases
}

// TestGoldenCorpus asserts the full parse -- identity and attributes together,
// not just whichever field last changed -- the same "assert the whole value"
// discipline the rest of this codebase's table tests already follow.
func TestGoldenCorpus(t *testing.T) {
	for _, c := range loadCorpus(t) {
		t.Run(c.Input, func(t *testing.T) {
			result := parse.Parse(c.Input)

			wantIdentity := parse.Identity{
				Artist: c.Artist, Album: c.Album, Year: c.Year, YearEnd: c.YearEnd,
				VariousArtists: c.VariousArtists, Discography: c.Discography,
			}
			if result.Identity != wantIdentity {
				t.Errorf("Identity = %+v, want %+v", result.Identity, wantIdentity)
			}

			if result.Attributes.Format != c.Format {
				t.Errorf("Format = %q, want %q", result.Attributes.Format, c.Format)
			}
			if result.Attributes.Lossless != c.Lossless {
				t.Errorf("Lossless = %v, want %v", result.Attributes.Lossless, c.Lossless)
			}
			if result.Attributes.Container != c.Container {
				t.Errorf("Container = %q, want %q", result.Attributes.Container, c.Container)
			}
			if result.Attributes.BitrateKbps != c.BitrateKbps {
				t.Errorf("BitrateKbps = %d, want %d", result.Attributes.BitrateKbps, c.BitrateKbps)
			}
			if result.Attributes.Source != c.Source {
				t.Errorf("Source = %q, want %q", result.Attributes.Source, c.Source)
			}
			if result.Attributes.Edition != c.Edition {
				t.Errorf("Edition = %q, want %q", result.Attributes.Edition, c.Edition)
			}
			if result.Attributes.ReleaseGroup != c.ReleaseGroup {
				t.Errorf("ReleaseGroup = %q, want %q", result.Attributes.ReleaseGroup, c.ReleaseGroup)
			}
			if result.Attributes.Discs != c.Discs {
				t.Errorf("Discs = %d, want %d", result.Attributes.Discs, c.Discs)
			}

			if result.Confidence < c.MinConfidence {
				t.Errorf("Confidence = %.2f, want at least %.2f", result.Confidence, c.MinConfidence)
			}
			if c.MaxConfidence != 0 && result.Confidence > c.MaxConfidence {
				t.Errorf("Confidence = %.2f, want at most %.2f", result.Confidence, c.MaxConfidence)
			}
		})
	}
}
