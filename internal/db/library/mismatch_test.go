package library_test

import (
	"testing"

	"github.com/makearr/MediaNexus/internal/db/library"
)

func TestCompareAttributes_NoMismatchWithinTolerance(t *testing.T) {
	claimed := library.AttributeClaim{AudioCodec: "flac", Lossless: true, BitrateKbps: 320, SampleRate: 44100}
	// Encoder rounding: a claimed 320 landing at 315 measured kbps is within
	// the 10% tolerance, and 44100 vs 44050 is within the 1% sample-rate one.
	measured := library.AttributeClaim{AudioCodec: "flac", Lossless: true, BitrateKbps: 315, SampleRate: 44050}

	got := library.CompareAttributes(claimed, measured)
	if len(got) != 0 {
		t.Errorf("CompareAttributes = %+v, want no mismatches within tolerance", got)
	}
	if json := library.MismatchJSON(got); json != "" {
		t.Errorf("MismatchJSON = %q, want empty for no mismatch", json)
	}
}

func TestCompareAttributes_CodecMismatchIsASignal(t *testing.T) {
	claimed := library.AttributeClaim{AudioCodec: "flac", Lossless: true}
	measured := library.AttributeClaim{AudioCodec: "mp3", Lossless: false}

	got := library.CompareAttributes(claimed, measured)
	codec, ok := got["audio_codec"]
	if !ok {
		t.Fatalf("CompareAttributes = %+v, want an audio_codec mismatch", got)
	}
	if codec.Claimed != "flac" || codec.Measured != "mp3" {
		t.Errorf("audio_codec mismatch = %+v, want claimed=flac measured=mp3", codec)
	}
	if _, ok := got["lossless"]; !ok {
		t.Error("a FLAC claim probing as MP3 must also disagree on losslessness")
	}
}

func TestCompareAttributes_BitrateBeyondToleranceIsAMismatch(t *testing.T) {
	claimed := library.AttributeClaim{AudioCodec: "mp3", BitrateKbps: 320}
	measured := library.AttributeClaim{AudioCodec: "mp3", BitrateKbps: 128} // a claimed upgrade that never happened

	got := library.CompareAttributes(claimed, measured)
	bitrate, ok := got["bitrate_kbps"]
	if !ok {
		t.Fatalf("CompareAttributes = %+v, want a bitrate_kbps mismatch", got)
	}
	if bitrate.Claimed != "320" || bitrate.Measured != "128" {
		t.Errorf("bitrate_kbps mismatch = %+v, want claimed=320 measured=128", bitrate)
	}

	encoded := library.MismatchJSON(got)
	if encoded == "" || encoded == "{}" {
		t.Errorf("MismatchJSON = %q, want a non-empty encoding of the mismatch", encoded)
	}
}

func TestCompareAttributes_MissingInformationIsNotAMismatch(t *testing.T) {
	// Neither side claims a bit depth or sample rate here -- absence must
	// never be reported as a disagreement.
	claimed := library.AttributeClaim{AudioCodec: "flac"}
	measured := library.AttributeClaim{AudioCodec: "flac"}

	if got := library.CompareAttributes(claimed, measured); len(got) != 0 {
		t.Errorf("CompareAttributes = %+v, want no mismatches when neither side claims a value", got)
	}
}
