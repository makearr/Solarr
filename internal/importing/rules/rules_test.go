package rules_test

import (
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/importing/rules"
)

func TestExtension(t *testing.T) {
	for file, want := range map[string]string{
		"Movie.2001.mkv":     "",
		"setup.EXE":          rules.RuleExecutableFile,
		"run.sh":             rules.RuleExecutableFile,
		"payload.ps1":        rules.RuleDangerousFile,
		"photo.lnk":          rules.RuleDangerousFile,
		"Movie.2001.nfo":     "",
		"Movie.2001.torrent": rules.RuleUserRejected,
	} {
		got := rules.Extension(file, []string{".torrent", "url"})
		switch {
		case want == "" && got != nil:
			t.Errorf("%q rejected as %s", file, got.Rule)
		case want != "" && (got == nil || got.Rule != want):
			t.Errorf("%q: %+v, want %s", file, got, want)
		}
	}
	if got := rules.Extension("a.url", []string{"url"}); got == nil || got.Rule != rules.RuleUserRejected {
		t.Errorf("an extension listed without a dot: %+v", got)
	}
}

func TestHiddenFile(t *testing.T) {
	if rules.HiddenFile("Movie (2001)/._Movie (2001).mkv") == nil || rules.HiddenFile("Movie (2001)/Movie (2001).mkv") != nil {
		t.Error("a file starting ._ is hidden, one that merely has a dot is not")
	}
}

func TestArchiveFoundNamesDangerFirst(t *testing.T) {
	if got := rules.ArchiveFound([]string{"a.rar", "b.exe"}); got == nil || got.Rule != rules.RuleExecutableFile {
		t.Errorf("exe beside a rar: %+v", got)
	}
	if got := rules.ArchiveFound([]string{"a.RAR", "a.nfo"}); got == nil || got.Rule != rules.RuleArchiveFound {
		t.Errorf("a rar: %+v", got)
	}
	if got := rules.ArchiveFound([]string{"a.mkv", "a.nfo"}); got != nil {
		t.Errorf("nothing wrong: %+v", got)
	}
}

func TestSampleByRuntimeTiers(t *testing.T) {
	cases := []struct {
		name  string
		facts rules.SampleFacts
		want  rules.Verdict
	}{
		{"a feature of 90 minutes, 2 minutes long", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: 2 * time.Minute, ExpectedMinutes: 90}, rules.VerdictSample},
		{"a feature, full length", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: 95 * time.Minute, ExpectedMinutes: 90}, rules.VerdictReal},
		{"just under ten minutes for a feature", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: 9*time.Minute + 59*time.Second, ExpectedMinutes: 120}, rules.VerdictSample},
		{"a half-hour show of six minutes", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: 6 * time.Minute, ExpectedMinutes: 30}, rules.VerdictReal},
		{"a half-hour show of four minutes", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: 4 * time.Minute, ExpectedMinutes: 30}, rules.VerdictSample},
		{"a webisode of two minutes", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: 2 * time.Minute, ExpectedMinutes: 8}, rules.VerdictReal},
		{"a short of ten seconds", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: 10 * time.Second, ExpectedMinutes: 2}, rules.VerdictSample},
		{"zero length", rules.SampleFacts{Path: "a.mkv", Probed: true, ExpectedMinutes: 90}, rules.VerdictSample},
		{"not probed", rules.SampleFacts{Path: "a.mkv", ExpectedMinutes: 90}, rules.VerdictUnknown},
		{"a disc image is never judged", rules.SampleFacts{Path: "a.iso", Probed: true, Runtime: time.Second, ExpectedMinutes: 90}, rules.VerdictReal},
		{"a stream file is never judged", rules.SampleFacts{Path: "a.strm", ExpectedMinutes: 90}, rules.VerdictReal},
		{"a special may be any length", rules.SampleFacts{Path: "a.mkv", Probed: true, Runtime: time.Minute, ExpectedMinutes: 45, Special: true}, rules.VerdictReal},
	}
	for _, c := range cases {
		if got := rules.Sample(c.facts); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if rules.RejectSample(rules.VerdictSample, true) != nil {
		t.Error("a file already in the library is never checked")
	}
	if got := rules.RejectSample(rules.VerdictUnknown, false); got == nil || got.Rule != rules.RuleSampleIndefinite {
		t.Errorf("unknown: %+v", got)
	}
	if got := rules.RejectSample(rules.VerdictSample, false); got == nil || got.Rule != rules.RuleSample {
		t.Errorf("sample: %+v", got)
	}
}

func TestHasAudio(t *testing.T) {
	if rules.HasAudio(true, 0) == nil || rules.HasAudio(true, 2) != nil || rules.HasAudio(false, 0) != nil {
		t.Error("only a probed file with no audio track is refused")
	}
}

func TestMultiPart(t *testing.T) {
	siblings := []string{"Movie (2001) cd2.mkv", "Movie (2001) cd1.mkv", "Movie (2001).nfo"}
	if rules.MultiPart("Movie (2001) cd1.mkv", siblings) == nil {
		t.Error("cd1 beside cd2 is one part of several")
	}
	if rules.MultiPart("Movie (2001) part A.mkv", []string{"Movie (2001) part A.mkv", "Movie (2001) part B.mkv"}) == nil {
		t.Error("lettered parts too")
	}
	if rules.MultiPart("Movie (2001) cd1.mkv", []string{"Movie (2001) cd1.mkv", "Other (2005).mkv"}) != nil {
		t.Error("a part token with no sibling part is a plain file")
	}
	if rules.MultiPart("Part 2 (1999).mkv", []string{"Part 2 (1999).mkv", "Part 3 (1999).mkv"}) != nil {
		t.Error("a title that begins with the token is not read as a part")
	}
	if rules.MultiPart("Heat (1995).mkv", []string{"Heat (1995).mkv", "Heat (1995) (2).mkv"}) != nil {
		t.Error("no token, no multi-part")
	}
}

func TestUnpackingAndFolderName(t *testing.T) {
	if got := rules.Unpacking("/dl/_UNPACK_Movie.2001/Movie.2001.mkv", false); got == nil || got.Rule != rules.RuleUnpacking {
		t.Errorf("a file inside an unpacking folder: %+v", got)
	}
	if rules.Unpacking("/dl/_FAILED_Movie/x.mkv", false) == nil {
		t.Error("a failed folder too")
	}
	if rules.Unpacking("/dl/Movie.2001/Movie.2001.mkv", false) != nil || rules.Unpacking("/dl/_UNPACK_x/y.mkv", true) != nil {
		t.Error("a finished folder, or a library file, is fine")
	}
	if got := rules.CleanFolderName("_UNPACK_Movie (2001)"); got != "Movie (2001)" {
		t.Errorf("clean name = %q", got)
	}
}

func TestFolderRemovable(t *testing.T) {
	if !rules.FolderRemovable([]rules.SourceFile{{Parsed: true, Sample: true}}, []int64{1 << 20}) {
		t.Error("only samples and a small stray archive left: removable")
	}
	if rules.FolderRemovable([]rules.SourceFile{{Parsed: true, Sample: false}}, nil) {
		t.Error("a real video left: keep")
	}
	if rules.FolderRemovable([]rules.SourceFile{{Parsed: false, Sample: true}}, nil) {
		t.Error("a video that cannot be read: keep")
	}
	if rules.FolderRemovable(nil, []int64{50 << 20}) {
		t.Error("a big archive left: keep")
	}
	if !rules.FolderRemovable(nil, nil) {
		t.Error("nothing left: removable")
	}
}

func TestEpisodeRules(t *testing.T) {
	if rules.FullSeason(true) == nil || rules.FullSeason(false) != nil || rules.SplitEpisode(true) == nil || rules.SplitEpisode(false) != nil {
		t.Error("full-season and split-episode names are refused")
	}
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	recent := []rules.EpisodeFacts{{AirDate: now.Add(-3 * time.Hour)}}
	old := []rules.EpisodeFacts{{AirDate: now.AddDate(0, 0, -10)}}
	if rules.MissingAbsolute(true, true, recent, now) == nil {
		t.Error("a just-aired anime episode with no absolute number waits")
	}
	if rules.MissingAbsolute(true, true, old, now) != nil || rules.MissingAbsolute(false, true, recent, now) != nil || rules.MissingAbsolute(true, false, recent, now) != nil {
		t.Error("an old episode, a non-anime series or a naming without the number is let through")
	}
	untitled := []rules.EpisodeFacts{{AirDate: now.Add(-3 * time.Hour), Title: " "}}
	tba := []rules.EpisodeFacts{{AirDate: now.Add(-3 * time.Hour), Title: "TBA"}}
	if got := rules.EpisodeTitle(rules.TitleAlways, true, false, untitled, 1, now); got == nil || got.Rule != rules.RuleTitleMissing {
		t.Errorf("untitled: %+v", got)
	}
	if got := rules.EpisodeTitle(rules.TitleAlways, true, false, tba, 1, now); got == nil || got.Rule != rules.RuleTitleTBA {
		t.Errorf("tba: %+v", got)
	}
	if rules.EpisodeTitle(rules.TitleNever, true, false, tba, 1, now) != nil || rules.EpisodeTitle(rules.TitleAlways, false, false, tba, 1, now) != nil ||
		rules.EpisodeTitle(rules.TitleAlways, true, true, tba, 1, now) != nil {
		t.Error("never, a naming without the title, or a library file: let through")
	}
	if rules.EpisodeTitle(rules.TitleAlways, true, false, []rules.EpisodeFacts{{AirDate: now.AddDate(0, 0, -5), Title: "TBA"}}, 1, now) != nil {
		t.Error("an episode that aired five days ago is let through")
	}
	if rules.EpisodeTitle(rules.TitleBulkSeasons, true, false, tba, 2, now) != nil {
		t.Error("bulk mode lets a few-episode day through")
	}
	if rules.EpisodeTitle(rules.TitleBulkSeasons, true, false, tba, 8, now) == nil {
		t.Error("bulk mode still waits for a bulk day's TBA")
	}
}

func TestEpisodeSets(t *testing.T) {
	if got := rules.Unexpected([]int{1, 2}, []int{2, 3}); len(got) != 1 || got[0] != 3 {
		t.Errorf("unexpected = %v", got)
	}
	if rules.Unexpected(nil, []int{9}) != nil {
		t.Error("no reference expects anything")
	}
	if rules.NotInRelease([]int{1}, []int{2}, false) == nil || rules.NotInRelease([]int{1}, []int{2}, true) != nil || rules.NotInRelease(nil, []int{2}, false) != nil {
		t.Error("an episode outside the grab is refused, unless it is a library file or there was no grab")
	}
	if rules.ExistingHasMore([]int{1}, [][]int{{1, 2}}) == nil {
		t.Error("replacing a two-episode file with a one-episode file is refused")
	}
	if rules.ExistingHasMore([]int{1, 2}, [][]int{{1, 2}}) != nil || rules.ExistingHasMore([]int{1}, nil) != nil {
		t.Error("the same episodes, or nothing on disk, is fine")
	}
}
