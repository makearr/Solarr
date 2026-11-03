package parsetv

import "testing"

func TestParseSubtitleFile(t *testing.T) {
	cases := []struct {
		name string
		want SubtitleInfo
		ok   bool
	}{
		{"Show.S01E01.en.srt", SubtitleInfo{Language: "en"}, true},
		{"Show.S01E01.German.forced.srt", SubtitleInfo{Language: "de", Forced: true}, true},
		{"Show S01E01.pt-BR.sdh.ass", SubtitleInfo{Language: "pt-BR", HearingImpaired: true}, true},
		{"Subs/Show.S01E01.1080p.srt", SubtitleInfo{}, true},
		{"English.Show.S01E01.srt", SubtitleInfo{}, true},
		{"Show.S01E01.mkv", SubtitleInfo{}, false},
	}
	for _, c := range cases {
		got, ok := ParseSubtitleFile(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %+v %v, want %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
