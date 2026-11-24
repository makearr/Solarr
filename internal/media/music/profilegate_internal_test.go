package music

import (
	"context"
	"testing"

	"github.com/makearr/Solarr/internal/identify/score"
	"github.com/makearr/Solarr/internal/importing/batch"
	"github.com/makearr/Solarr/internal/importing/inspect"
	"github.com/makearr/Solarr/internal/platform/mediatool"
)

func TestNamesAFormat_OnlyAStatedCodecBitrateOrLosslessCounts(t *testing.T) {
	for title, want := range map[string]bool{
		"Vessa Lund - Quiet Harbour (2016) [FLAC]":      true,
		"Vessa Lund - Quiet Harbour (2016) MP3 320":     true,
		"Vessa Lund - Quiet Harbour (2016) V0":          true,
		"Vessa Lund - Quiet Harbour (2016) CD Lossless": false,
		"Vessa Lund - Quiet Harbour (2016)":             false,
		"Vessa Lund - Quiet Harbour 2CD":                false,
	} {
		if got := NamesAFormat(title); got != want {
			t.Errorf("%q: %v, want %v", title, got, want)
		}
	}
}

// The probe decides for a release that named nothing: MP3 files in a lossless-only library are refused, FLAC passes.
func TestProbedFilesFit_AnMp3InALosslessOnlyLibraryDoesNotFit(t *testing.T) {
	losslessOnly := func(_ int64, facts score.Facts) (bool, bool) { return facts.File.Lossless, true }
	importer := NewImporter(ImporterConfig{Profile: &ProfileGate{
		Claimless: func(context.Context, int64) bool { return true }, Accepts: losslessOnly,
	}})
	unit := inspect.Unit{Media: []inspect.File{{RelPath: "01.mp3"}, {RelPath: "02.flac"}}}
	relPaths := []string{"01.mp3", "02.flac"}
	mp3 := mediatool.Probe{Measured: mediatool.Measured{AudioCodec: "mp3", AudioBitrate: 320000}}
	flac := mediatool.Probe{Measured: mediatool.Measured{AudioCodec: "flac"}}
	request := batch.Request{CategoryID: 3}
	if importer.probedFilesFit(request, unit, relPaths, []mediatool.Probe{mp3, flac}) {
		t.Error("an MP3 among the files: fits, want refused")
	}
	if !importer.probedFilesFit(request, inspect.Unit{Media: []inspect.File{{RelPath: "02.flac"}}}, []string{"02.flac"}, []mediatool.Probe{flac}) {
		t.Error("only FLAC: refused, want it fits")
	}
	unjudged := NewImporter(ImporterConfig{Profile: &ProfileGate{Accepts: func(int64, score.Facts) (bool, bool) { return false, false }}})
	if !unjudged.probedFilesFit(request, unit, relPaths, []mediatool.Probe{mp3, flac}) {
		t.Error("a library with no profile to judge by: refused, want it fits")
	}
}
