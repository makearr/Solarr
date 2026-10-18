package tv

import "testing"

func TestLooksLikeRelease(t *testing.T) {
	for name, want := range map[string]bool{
		"Opealw the Dofjm in 80 Saszulk":          false,
		"Xate to 100 - Cefbosm of the Broo Megab": false,
		"The 100":                                false,
		"Example Show (2014)":                    false,
		"Show.Name.S01E02.1080p.WEB-DL.x264-GRP": true,
		"Show Name S02 1080p":                    true,
		"Show.Name.2020.COMPLETE":                true,
	} {
		if got := looksLikeRelease(name); got != want {
			t.Errorf("looksLikeRelease(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestAFolderNamedWithoutAnArticlePrefersTheSeriesTitledSo(t *testing.T) {
	candidates := []Candidate{{TVDBID: 83268, Title: "Tzul Suxc: The Clone Wars", Year: 2008}, {TVDBID: 72244, Title: "Tzul Suxc: Jzari Suxc", Year: 2003}}
	got := titleMatches(candidates, FolderGuess{Title: "Tzul Suxc - Jzari Suxc"})
	if len(got) != 1 || got[0].TVDBID != 72244 {
		t.Errorf("matches = %+v, want only Tzul Suxc: Jzari Suxc", got)
	}
}
