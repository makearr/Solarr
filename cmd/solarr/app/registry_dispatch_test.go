package app

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/makearr/Solarr/internal/acquiring/plan"
	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/media"
)

// huntingType is a media type that only records which items it was asked to hunt.
type huntingType struct {
	key    media.Key
	hunted *[]int64
}

func (h huntingType) Descriptor() media.Descriptor { return media.Descriptor{Key: h.key} }

func (h huntingType) HuntSearch(_ context.Context, itemID int64) (plan.Outcome, error) {
	*h.hunted = append(*h.hunted, itemID)
	return plan.OutcomeSearched, nil
}

// wantedItems lists the seeded wanted items of one media type, oldest first.
func wantedItems(t *testing.T, server goldenServer, mediaType string) []int64 {
	t.Helper()
	rows, err := server.process.library.Read().QueryContext(t.Context(),
		`SELECT d.id FROM desired_item d JOIN work w ON w.id = d.work_id
		 WHERE w.media_type = ? AND d.monitored = 1 AND d.state IN ('MISSING', 'PARTIAL') ORDER BY d.id`, mediaType)
	if err != nil {
		t.Fatalf("query desired items: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil || len(ids) == 0 {
		t.Fatalf("no wanted %s item was seeded: %v", mediaType, err)
	}
	return ids
}

// The hunt hands an item to the media type of the library it is wanted in, and to no other.
func TestHuntSearch_HandsAnItemToItsLibrarysMediaType(t *testing.T) {
	server := newGoldenServer(t)
	server.seedLibraries(t)
	album, season := wantedItems(t, server, "music")[0], wantedItems(t, server, "tv")[0]

	var music, television []int64
	registry, err := media.NewRegistry(huntingType{"music", &music}, huntingType{"tv", &television})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	server.process.media = registry

	for _, itemID := range []int64{album, season} {
		outcome, err := server.process.huntSearch(t.Context(), itemID)
		if err != nil || outcome != plan.OutcomeSearched {
			t.Fatalf("huntSearch(%d) = %v, %v, want searched", itemID, outcome, err)
		}
	}
	if got, want := [][]int64{music, television}, [][]int64{{album}, {season}}; !reflect.DeepEqual(got, want) {
		t.Errorf("items handed to (music, tv) = %v, want %v", got, want)
	}
}

// A library whose media type cannot hunt is not searched, and that is not an error.
func TestHuntSearch_LeavesAnItemWhoseMediaTypeCannotHunt(t *testing.T) {
	server := newGoldenServer(t)
	server.seedLibraries(t)
	album := wantedItems(t, server, "music")[0]

	var television []int64
	registry, err := media.NewRegistry(huntingType{"tv", &television})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	server.process.media = registry

	outcome, err := server.process.huntSearch(t.Context(), album)
	if err != nil || outcome != plan.OutcomeNotWanted {
		t.Errorf("huntSearch = %v, %v, want not wanted and no error", outcome, err)
	}
	if len(television) != 0 {
		t.Errorf("the TV type was handed %v, want nothing", television)
	}
}

// labellingType names a release by its item reference, so the label is recognisable.
type labellingType struct{ roles []media.Role }

func (l labellingType) Descriptor() media.Descriptor {
	return media.Descriptor{Key: "labelled", Roles: l.roles}
}

func (labellingType) ReleaseLabel(_, itemRef string, _ time.Time) string { return "label:" + itemRef }

func TestLeadName_IsTheCreditOfTheTypesLeadRole(t *testing.T) {
	credits := []library.Credit{{Role: "writer", PersonName: "A Writer"}, {Role: "author", PersonName: "An Author"}}
	withLead, err := media.NewRegistry(labellingType{roles: []media.Role{{Key: "writer"}, {Key: "author", Lead: true}}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	withoutLead, err := media.NewRegistry(labellingType{roles: []media.Role{{Key: "writer"}}})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	got := [3]string{
		leadName(withLead, "labelled", credits), leadName(withoutLead, "labelled", credits), leadName(withLead, "absent", credits),
	}
	if want := [3]string{"An Author", "", ""}; got != want {
		t.Errorf("leadName (lead role, no lead role, unknown type) = %q, want %q", got, want)
	}
}
