package app

import (
	"slices"
	"testing"

	"github.com/makearr/Solarr/internal/media"
)

func TestAdultOnlyKeepsTheKindsThatAreAdult(t *testing.T) {
	descriptors := []media.Descriptor{
		{Key: "comic", Kinds: []media.LibraryKind{{Key: "comic"}, {Key: "hentai", Adult: true}}},
		{Key: "adult", Kinds: []media.LibraryKind{{Key: "scene"}, {Key: "film"}}},
		{Key: "book", Kinds: []media.LibraryKind{{Key: "novel"}}},
	}
	types, kinds := adultOnly(descriptors, []string{"comic", "book"}, nil)
	if !slices.Equal(types, []string{"comic"}) || !slices.Equal(kinds, []string{"hentai"}) {
		t.Errorf("types %v kinds %v: a type with no adult kind is dropped and a mixed one keeps its adult kind", types, kinds)
	}
	types, kinds = adultOnly(descriptors, []string{"adult"}, []string{"film"})
	if !slices.Equal(types, []string{"adult"}) || !slices.Equal(kinds, []string{"film"}) {
		t.Errorf("types %v kinds %v: the adult type is all adult, the declared kinds still narrow it", types, kinds)
	}
	if types, _ = adultOnly(descriptors, []string{"comic"}, []string{"comic"}); len(types) != 0 {
		t.Errorf("a definition declared for a non-adult kind only is offered nowhere: %v", types)
	}
}
