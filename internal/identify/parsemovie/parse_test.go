package parsemovie_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/identify/parsemovie"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		year    int
		edition string
		group   string
	}{
		{"The.Example.Film.1999.1080p.BluRay.x264-GROUP", "The Example Film", 1999, "", "GROUP"},
		{"The Example Film (1999)", "The Example Film", 1999, "", ""},
		{"The Example Film (1999).mkv", "The Example Film", 1999, "", ""},
		{"Sample.Runner.2049.2017.2160p.UHD.BluRay.x265-TERMINAL", "Sample Runner 2049", 2017, "", "TERMINAL"},
		{"2001.A.Cfago.Utenkoa.1968.1080p.BluRay.x264-AMIABLE", "2001 A Cfago Utenkoa", 1968, "", "AMIABLE"},
		{"1917.2019.1080p.BluRay.x264-SPARKS", "1917", 2019, "", "SPARKS"},
		{"1917 (2019)", "1917", 2019, "", ""},
		{"2012.2009.720p.BluRay.x264", "2012", 2009, "", ""},
		{"10,000 XS (2008).mp4", "10,000 XS", 2008, "", ""},
		{"Xio Afgumm! The Koffbi Dotuo (2018).mkv", "Xio Afgumm! The Koffbi Dotuo", 2018, "", ""},
		{"Sample.Runner.1982.Final.Cut.1080p.BluRay.x264-GRP", "Sample Runner", 1982, "Final Cut", "GRP"},
		{"Utiokm.1986.Directors.Cut.1080p.BluRay.x264-GRP", "Utiokm", 1986, "Director's Cut", "GRP"},
		{"Alibaxuwgi Sed (1979) {edition-Redux}", "Alibaxuwgi Sed", 1979, "Redux", ""},
		{"Cige.2021.IMAX.2160p.WEB-DL.DDP5.1.Atmos.HDR.H.265-FLUX", "Cige", 2021, "IMAX", "FLUX"},
		{"The.Fozb.of.the.Sewcd.The.Davinn.of.the.Kosl.2003.EXTENDED.1080p.BluRay.x264-GRP", "The Fozb of the Sewcd The Davinn of the Kosl", 2003, "Extended", "GRP"},
		{"Movie Title (2010) [1080p] [BluRay] [YTS.MX]", "Movie Title", 2010, "", ""},
		{"Some.Movie.2015.MULTi.1080p.WEB.H264-NOGRP", "Some Movie", 2015, "", ""},
		{"Pazegamu.2019.KOREAN.1080p.BluRay.x264-GRP", "Pazegamu", 2019, "", "GRP"},
		{"Fhuhetur.Ojue.2001.720p.BluRay.x264-GRP", "Fhuhetur Ojue", 2001, "", "GRP"},
		{"Ewadui (2001)", "Ewadui", 2001, "", ""},
		{"Wjibxajmu's Leb (2006)", "Wjibxajmu's Leb", 2006, "", ""},
		{"Tixihu Leb (2024)", "Tixihu Leb", 2024, "", ""},
		{"Nusjbin or (The Izuxmatnov Tecmeu of Omnesaxho) (2014)", "Nusjbin or (The Izuxmatnov Tecmeu of Omnesaxho)", 2014, "", ""},
		{"Ebulo Hugtel 3 ()", "Ebulo Hugtel 3", 0, "", ""},
		{"No Year Movie 1080p BluRay x264-GRP", "No Year Movie", 0, "", "GRP"},
		{"Uvvurwaih", "Uvvurwaih", 0, "", ""},
		{"Uncut Example (2019) 1080p BluRay", "Uncut Example", 2019, "", ""},
		{"The.Solut.Tah.Example.2004.1080p.BluRay.x264-GRP", "The Solut Tah Example", 2004, "", "GRP"},
	}
	for _, c := range cases {
		info, ok := parsemovie.Parse(c.name)
		if !ok {
			t.Errorf("%q: not parsed", c.name)
			continue
		}
		if info.Title != c.title || info.Year != c.year || info.Edition != c.edition {
			t.Errorf("%q: title %q year %d edition %q, want %q %d %q", c.name, info.Title, info.Year, info.Edition, c.title, c.year, c.edition)
		}
		if c.group != "" && info.ReleaseGroup != c.group {
			t.Errorf("%q: group %q, want %q", c.name, info.ReleaseGroup, c.group)
		}
	}
}

func TestParseIDsAndClaims(t *testing.T) {
	info, _ := parsemovie.Parse("The Example Film (1999) {imdb-tt0000001} {tmdb-603} [1080p]")
	if info.IMDb != "tt0000001" || info.TMDB != 603 || info.Title != "The Example Film" || info.Year != 1999 {
		t.Errorf("ids: %+v", info)
	}
	claims, _ := parsemovie.Parse("Cige.2021.2160p.WEB-DL.DDP5.1.HDR.H.265-FLUX")
	if claims.Claims.Resolution != 2160 || claims.Claims.Source != "webdl" || claims.Claims.VideoCodec != "h265" {
		t.Errorf("claims: %+v", claims.Claims)
	}
}

func TestParseAlternativeTitles(t *testing.T) {
	info, _ := parsemovie.Parse("La.Vita.e.Bella.AKA.Misu.Is.Kieolimet.1997.1080p.BluRay.x264-GRP")
	if info.Year != 1997 || len(info.AllTitles) != 2 || info.AllTitles[1] != "Misu Is Kieolimet" {
		t.Errorf("alternatives: %+v", info)
	}
}

func TestParsePathTakesTheFolderWhenTheFileHasNoYear(t *testing.T) {
	info, ok := parsemovie.ParsePath("Heat (1995)/movie.1080p.mkv")
	if !ok || info.Title != "Heat" || info.Year != 1995 || info.Claims.Resolution != 1080 {
		t.Errorf("folder: %+v ok=%v", info, ok)
	}
	info, ok = parsemovie.ParsePath("Heat (1995)/Heat (1995).en.srt")
	if !ok || info.Title != "Heat" || info.Year != 1995 {
		t.Errorf("file: %+v ok=%v", info, ok)
	}
}
