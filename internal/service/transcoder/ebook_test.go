package transcoder_test

import (
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/service/transcoder"
)

func TestAnalyzeEbook_NeedsOnlyAFileOfAnotherFormatAndNeverGuessesAnUnsupportedTarget(t *testing.T) {
	epub := settings.QualityTarget{MediaType: "book", Container: "epub"}
	for name, test := range map[string]struct {
		file    string
		target  settings.QualityTarget
		verdict transcoder.Verdict
		lane    transcoder.Lane
	}{
		"text becomes epub":   {"A Tale.txt", epub, transcoder.Needed, transcoder.LaneBooks},
		"an epub is left":     {"A Tale.EPUB", epub, transcoder.NotNeeded, ""},
		"a target of mobi":    {"A Tale.txt", settings.QualityTarget{MediaType: "book", Container: "mobi"}, transcoder.NotNeeded, ""},
		"a docx wants pandoc": {"A Tale.docx", epub, transcoder.Needed, transcoder.LaneBooks},
	} {
		plan := transcoder.AnalyzeEbook(test.file, test.target)
		if plan.Verdict != test.verdict || plan.Fixed != test.lane {
			t.Errorf("%s: verdict %q lane %q, want %q %q", name, plan.Verdict, plan.Fixed, test.verdict, test.lane)
		}
	}
}

func TestTranscodeKind_ABookTargetWithAFormatIsEbook(t *testing.T) {
	got := []string{
		settings.QualityTarget{MediaType: "book", Container: "epub"}.TranscodeKind(),
		settings.QualityTarget{MediaType: "book"}.TranscodeKind(),
	}
	if want := []string{"ebook", "other"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kinds = %v, want %v", got, want)
	}
}
