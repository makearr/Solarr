package library_test

import (
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
)

func resolveNamed(t *testing.T, people library.People, provider, id, name, born string) int64 {
	t.Helper()
	personID, err := people.ResolvePerson(t.Context(), firstSeen, library.PersonLink{
		Provider: provider, ExternalID: id, Person: library.PersonIdentity{Name: name, Born: born},
	})
	if err != nil {
		t.Fatalf("ResolvePerson %s:%s: %v", provider, id, err)
	}
	return personID
}

// renamed makes a duplicate the way a provider's later details do: the person is called by another name first.
func renamed(t *testing.T, people library.People, provider, id, name, born string, aliases ...string) int64 {
	t.Helper()
	personID := resolveNamed(t, people, provider, id, "Placeholder "+id, "")
	if err := people.BackfillDetails(t.Context(), personID, library.PersonIdentity{Name: name, Born: born, Aliases: aliases}); err != nil {
		t.Fatalf("BackfillDetails: %v", err)
	}
	return personID
}

func exists(t *testing.T, people library.People, id int64) bool {
	t.Helper()
	_, err := people.Person(t.Context(), id)
	return err == nil
}

func TestResolvePerson_JoinsTheOneSameNamedPersonOfAnotherProvider(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	first := resolveNamed(t, people, "alpha:person", "a1", "Zela Moru", "1990-04-02")
	second := resolveNamed(t, people, "beta:person", "b1", "zela moru", "1990")
	if second != first {
		t.Fatalf("other provider's id made person %d, want %d", second, first)
	}
	if found, ok, err := people.FindByExternalID(t.Context(), "beta:person", "b1"); err != nil || !ok || found != first {
		t.Fatalf("FindByExternalID = %d, %v, %v; want %d", found, ok, err, first)
	}
}

func TestResolvePerson_JoinsByAliasAndKeepsApartWhatContradicts(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	known := renamed(t, people, "alpha:person", "a1", "Zela Moru", "1990", "Zee")
	if joined := resolveNamed(t, people, "beta:person", "b1", "Zee", ""); joined != known {
		t.Fatalf("alias match made person %d, want %d", joined, known)
	}
	if apart := resolveNamed(t, people, "gamma:person", "g1", "Zela Moru", "1975"); apart == known {
		t.Fatal("a person born in another year joined")
	}
	if same := resolveNamed(t, people, "alpha:person", "a2", "Zela Moru", ""); same == known {
		t.Fatal("two ids of one provider became one person")
	}
}

func TestConvergePeople_MergesTheUncontestedAndSplitsTheContestedByBirth(t *testing.T) {
	opened, _ := newLibrary(t)
	people := library.NewPeople(opened)

	one, other := renamed(t, people, "alpha:person", "a1", "Zela Moru", ""), renamed(t, people, "beta:person", "b1", "Zela Moru", "")
	oldA, oldB := renamed(t, people, "alpha:person", "a2", "Nuvi Kest", "1970-01-01"), renamed(t, people, "alpha:person", "a3", "Nuvi Kest", "1990")
	oldC, unknown := renamed(t, people, "beta:person", "b2", "Nuvi Kest", "1990-05-05"), renamed(t, people, "gamma:person", "g1", "Nuvi Kest", "")
	aliased, named := renamed(t, people, "alpha:person", "a4", "Rho Tavu", "", "Rho T"), renamed(t, people, "beta:person", "b3", "Rho T", "")

	merged, err := people.ConvergePeople(t.Context())
	if err != nil {
		t.Fatalf("ConvergePeople: %v", err)
	}
	if merged != 3 {
		t.Fatalf("merged %d people, want 3", merged)
	}
	survivors := func(ids ...int64) int {
		count := 0
		for _, id := range ids {
			if exists(t, people, id) {
				count++
			}
		}
		return count
	}
	if got := survivors(one, other); got != 1 {
		t.Fatalf("%d of the uncontested pair remain, want 1", got)
	}
	if got := survivors(oldB, oldC); got != 1 {
		t.Fatalf("%d of the pair born in 1990 remain, want 1", got)
	}
	if !exists(t, people, oldA) || !exists(t, people, unknown) {
		t.Fatal("a person of another or an unknown birth was merged")
	}
	if got := survivors(aliased, named); got != 1 {
		t.Fatalf("%d of the alias pair remain, want 1", got)
	}
}
