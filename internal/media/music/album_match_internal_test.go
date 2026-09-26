package music

import (
	"testing"
)

func discTagged(disc, track int, title string) LocalTrack {
	return LocalTrack{Disc: disc, Track: track, Title: Claim{Value: title, Provenance: ProvenanceTag}}
}

// NETA' "Kick": three discs tagged disc 3, every number shared by different songs.
func TestWithoutCollidingDiscs_ASetTaggedOneDiscThroughoutAbstains(t *testing.T) {
	locals := []LocalTrack{
		discTagged(3, 1, "Fuku in the Zet"), discTagged(3, 1, "Move On"), discTagged(3, 1, "Never Zadu Us Tuhuh"),
		discTagged(3, 2, "Vap Tubivedut"), discTagged(3, 2, "I'm Coming"), discTagged(3, 2, "Vap Tubivedut (mix)"),
	}
	for i, local := range withoutCollidingDiscs(locals) {
		if local.Disc != 0 {
			t.Errorf("file %d keeps disc %d, want every disc claim dropped", i, local.Disc)
		}
	}
}

// "Hazorihelo the Mahopep": duplicate copies of songs and one stray bonus share numbers with
// few other files; the disc claims stand.
func TestWithoutCollidingDiscs_DuplicateCopiesKeepTheirDiscs(t *testing.T) {
	locals := []LocalTrack{
		discTagged(1, 1, "Stay Young"), discTagged(1, 2, "Hezukanu"), discTagged(1, 3, "Big Go Go"),
		discTagged(1, 4, "Hazorihelo the Mahopep"), discTagged(1, 4, "Hazorihelo The Mahopep"),
		discTagged(1, 5, "Fair Weather Ahead"), discTagged(1, 6, "Vicip of Butohehas"), discTagged(1, 6, "Vicip Of Butohehas"),
		discTagged(1, 7, "Follow"), discTagged(1, 8, "Ralakipan"), discTagged(1, 9, "What Would You Do?"),
		discTagged(1, 10, "Just to Learn Again"), discTagged(1, 10, "Just To Learn Again"), discTagged(1, 10, "All Nomof Pevos of Lugusapo"),
	}
	for i, local := range withoutCollidingDiscs(locals) {
		if local.Disc != 1 {
			t.Errorf("file %d has disc %d, want its own disc 1 kept", i, local.Disc)
		}
	}
}
