package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

var performerProviders = []string{"alpha:performer", "beta:performer"}

func resolveNamed(t *testing.T, people library.People, provider, id, name string) int64 {
	t.Helper()
	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: provider, ExternalID: id, Person: library.PersonIdentity{Name: name}, JoinProviders: performerProviders,
	})
	if err != nil {
		t.Fatalf("ResolvePerson %s:%s: %v", provider, id, err)
	}
	return personID
}

func TestResolvePerson_JoinsTheOneSameNamedPersonOfAnotherProvider(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	first := resolveNamed(t, people, "alpha:performer", "a1", "Zela Moru")
	second := resolveNamed(t, people, "beta:performer", "b1", "zela moru")
	if second != first {
		t.Fatalf("other provider's id made person %d, want %d", second, first)
	}
	if again, found, err := people.FindByExternalID(t.Context(), "beta:performer", "b1"); err != nil || !found || again != first {
		t.Fatalf("FindByExternalID = %d, %v, %v; want %d", again, found, err, first)
	}
}

func TestResolvePerson_KeepsApartAnIdOfTheProviderTheNameAlreadyHolds(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	first := resolveNamed(t, people, "alpha:performer", "a1", "Zela Moru")
	second := resolveNamed(t, people, "alpha:performer", "a2", "Zela Moru")
	if second == first {
		t.Fatal("two ids of one provider became one person")
	}
}

func TestMergeSameNamed_FoldsDisjointProvidersAndLeavesSharedOnesApart(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	plain := func(provider, id, name string) int64 {
		personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
			Provider: provider, ExternalID: id, Person: library.PersonIdentity{Name: name},
		})
		if err != nil {
			t.Fatalf("ResolvePerson: %v", err)
		}
		return personID
	}
	one, other := plain("alpha:performer", "a1", "Zela Moru"), plain("beta:performer", "b1", "Zela Moru")
	twinA, twinB, twinC := plain("alpha:performer", "a2", "Nuvi Kest"), plain("alpha:performer", "a3", "Nuvi Kest"), plain("beta:performer", "b2", "Nuvi Kest")

	merged, err := people.MergeSameNamed(t.Context(), performerProviders)
	if err != nil {
		t.Fatalf("MergeSameNamed: %v", err)
	}
	if merged != 1 {
		t.Fatalf("merged %d people, want 1", merged)
	}
	survivors := 0
	for _, id := range []int64{one, other} {
		if _, err := people.Person(t.Context(), id); err == nil {
			survivors++
		}
	}
	if survivors != 1 {
		t.Fatalf("%d of the two same-named people remain, want 1", survivors)
	}
	for _, id := range []int64{twinA, twinB, twinC} {
		if _, err := people.Person(t.Context(), id); err != nil {
			t.Fatalf("person %d with a shared provider was merged: %v", id, err)
		}
	}
}
