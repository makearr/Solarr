package parse_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/makearr/Solarr/internal/identify/model"
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
	BitrateMode    string  `json:"bitrate_mode"`
	VBRPreset      string  `json:"vbr_preset"`
	SampleRate     int     `json:"sample_rate"`
	BitDepth       int     `json:"bit_depth"`
	ProperRepack   bool    `json:"proper_repack"`
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

			// Compared whole, so a field added to model.Attributes is asserted by
			// every fixture from the moment it exists rather than when someone
			// remembers to list it here.
			wantAttributes := model.Attributes{
				Format: c.Format, Lossless: c.Lossless, BitrateKbps: c.BitrateKbps,
				BitrateMode: c.BitrateMode, VBRPreset: c.VBRPreset, SampleRate: c.SampleRate,
				Container: c.Container, Source: c.Source, Edition: c.Edition,
				ReleaseGroup: c.ReleaseGroup, Discs: c.Discs, BitDepth: c.BitDepth,
				ProperRepack: c.ProperRepack,
			}
			if result.Attributes != wantAttributes {
				t.Errorf("Attributes = %+v, want %+v", result.Attributes, wantAttributes)
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
