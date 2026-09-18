package pack_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/importing/pack"
)

// The source itself declares what it is -- "Artist-Discography-1990-2020-
// FLAC" with 14 album folders, ADR-50's own worked example. Every direct
// child becomes a member regardless of its own name.
func TestDetect_SourceDeclaresDiscography(t *testing.T) {
	candidates := []pack.Candidate{
		{UnitID: 1, RelPath: "1977 (2022)"},
		{UnitID: 2, RelPath: "Bim Padu Bim (2003)"},
		{UnitID: 3, RelPath: "Lebavukomut (1995)"},
	}
	groups := pack.Detect("XT+QK-Discography-1975-2020-FLAC", candidates)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want 1", groups)
	}
	g := groups[0]
	if g.Kind != pack.Discography || g.DetectedFrom != pack.DetectedFromStructureAndTitle {
		t.Errorf("Kind/DetectedFrom = %s/%s, want DISCOGRAPHY/structure_and_name", g.Kind, g.DetectedFrom)
	}
	if len(g.Members) != 3 {
		t.Errorf("Members = %+v, want all 3", g.Members)
	}
}

// No declared-discography container: two releases by the same artist
// happened to land in one flat scan (a real fixture shape) still group,
// found by their own parsed artist agreeing.
func TestDetect_SiblingArtistAgreement(t *testing.T) {
	candidates := []pack.Candidate{
		{UnitID: 1, RelPath: "Lamig Vu Linumudo-Cedak Tivilute-CDM-FLAC-1996-CHS"},
		{UnitID: 2, RelPath: "Lamig Vu Linumudo-The Mukaminu Rerer-SINGLE-16BIT-WEB-FLAC-2018-FLACED"},
		{UnitID: 3, RelPath: "Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
	}
	groups := pack.Detect("incoming", candidates)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want 1 (only the two Weird Al releases)", groups)
	}
	if len(groups[0].Members) != 2 {
		t.Fatalf("Members = %+v, want 2", groups[0].Members)
	}
	ids := map[int64]bool{groups[0].Members[0].UnitID: true, groups[0].Members[1].UnitID: true}
	if !ids[1] || !ids[2] {
		t.Errorf("Members = %+v, want unit 1 and 2 (the Weird Al releases), not Vutu Doci", groups[0].Members)
	}
}

// A single unit, named "Anthology" or anything else, never becomes a pack --
// the trap falls out of requiring at least two siblings, not from reading
// the name (§19.6).
func TestDetect_AnthologyTrapIsASingleUnit(t *testing.T) {
	candidates := []pack.Candidate{{UnitID: 1, RelPath: "Anthology"}}
	groups := pack.Detect("incoming", candidates)
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none for a single unit", groups)
	}
}

// A unit nested under its own subdirectory (already merged by GROUP into a
// real Artist/Album structure) is not this detector's shape -- excluded
// even when two such units happen to share a parsed artist, since a
// two-level Artist/Album layout is an organised library, not one bundled
// source.
func TestDetect_NestedUnitsExcluded(t *testing.T) {
	candidates := []pack.Candidate{
		{UnitID: 1, RelPath: "Vutu Doci/The Sezo Siluko"},
		{UnitID: 2, RelPath: "Vutu Doci/Cotevo of Love"},
	}
	groups := pack.Detect("music", candidates)
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none (nested units excluded)", groups)
	}
}

// A real live-found case: GROUP's track-continuity fallback split one real
// album (messy filenames, no clean sequential numbering) into five
// import_unit rows all reporting the same directory. Every one of them must
// be excluded from sibling grouping -- treating five fragments of one album
// as five different albums by the same artist is worse than detecting
// nothing at all.
func TestDetect_ExcludesUnitsSharingOneDirectory(t *testing.T) {
	candidates := []pack.Candidate{
		{UnitID: 1, RelPath: "Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
		{UnitID: 2, RelPath: "Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
		{UnitID: 3, RelPath: "Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
		{UnitID: 4, RelPath: "Vutu Doci-Cotevo of Love-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
	}
	groups := pack.Detect("incoming", candidates)
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none (only one clean directory remains)", groups)
	}
}

// The same shape, but with a real second clean release: the ambiguous
// fragments are excluded, the two clean, distinct releases still group.
func TestDetect_StillGroupsCleanSiblingsAlongsideAnAmbiguousDirectory(t *testing.T) {
	candidates := []pack.Candidate{
		{UnitID: 1, RelPath: "Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
		{UnitID: 2, RelPath: "Vutu Doci-The Sezo Siluko-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
		{UnitID: 3, RelPath: "Vutu Doci-Cotevo of Love-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
		{UnitID: 4, RelPath: "Vutu Doci-Zipur for Rigu-Remastered-24BIT-WEB-FLAC-2018-TiMES"},
	}
	groups := pack.Detect("incoming", candidates)
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want 1 (the two clean releases)", groups)
	}
	if len(groups[0].Members) != 2 {
		t.Fatalf("Members = %+v, want 2", groups[0].Members)
	}
	ids := map[int64]bool{groups[0].Members[0].UnitID: true, groups[0].Members[1].UnitID: true}
	if !ids[3] || !ids[4] {
		t.Errorf("Members = %+v, want units 3 and 4, not the ambiguous 1/2", groups[0].Members)
	}
}

// Fewer than two direct children never groups, even with a declared
// discography container -- nothing to relate a lone member to.
func TestDetect_ASingleDirectChildNeverGroups(t *testing.T) {
	candidates := []pack.Candidate{{UnitID: 1, RelPath: "1977 (2022)"}}
	groups := pack.Detect("XT+QK-Discography-1975-2020-FLAC", candidates)
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none", groups)
	}
}
