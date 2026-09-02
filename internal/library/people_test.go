package library_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/makearr/MediaNexus/internal/store"
	"github.com/makearr/MediaNexus/internal/store/gen/librarydb"
)

func createPerson(t *testing.T, opened *store.Store, name string) librarydb.Person {
	t.Helper()
	var person librarydb.Person
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreatePerson(ctx, librarydb.CreatePersonParams{
			Name: name, SortName: name, AddedAt: 1,
		})
		person = row
		return err
	})
	return person
}

func createOrg(t *testing.T, opened *store.Store, name string, parent int64) librarydb.Organisation {
	t.Helper()
	var org librarydb.Organisation
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateOrganisation(ctx, librarydb.CreateOrganisationParams{
			Name: name, SortName: name, Kind: "label",
			ParentID: sql.NullInt64{Int64: parent, Valid: parent > 0},
		})
		org = row
		return err
	})
	return org
}

func createTag(t *testing.T, opened *store.Store, slug string, parent int64) librarydb.Tag {
	t.Helper()
	var tag librarydb.Tag
	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		row, err := q.CreateTag(ctx, librarydb.CreateTagParams{
			Name: slug, Slug: slug, Source: "user",
			ParentID: sql.NullInt64{Int64: parent, Valid: parent > 0},
		})
		if err != nil {
			return err
		}
		tag = row
		if err := q.AddTagSelfClosure(ctx, librarydb.AddTagSelfClosureParams{
			AncestorID: row.ID, DescendantID: row.ID,
		}); err != nil {
			return err
		}
		if parent > 0 {
			return q.AddTagClosure(ctx, librarydb.AddTagClosureParams{
				DescendantID: row.ID, ParentID: parent,
			})
		}
		return nil
	})
	return tag
}

// F13, and no competitor has it: subscribing to Htuacmasn as director and not as
// producer needs the role on the credit, not on the person.
func TestCreditsAreQueryableByRole(t *testing.T) {
	opened, queries := newLibrary(t)
	person := createPerson(t, opened, "Steven Htuacmasn")
	directed := createWork(t, opened, "movie", "Habf")
	produced := createWork(t, opened, "movie", "Betfukdiadv")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, entry := range []struct {
			work int64
			role string
		}{{directed.ID, "director"}, {produced.ID, "producer"}} {
			if err := q.AddCredit(ctx, librarydb.AddCreditParams{
				PersonID: person.ID, WorkID: entry.work, Role: entry.role,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	asDirector, err := queries.ListWorksCreditedTo(t.Context(),
		librarydb.ListWorksCreditedToParams{PersonID: person.ID, Role: "director"})
	if err != nil {
		t.Fatalf("ListWorksCreditedTo: %v", err)
	}
	if len(asDirector) != 1 || asDirector[0].ID != directed.ID {
		t.Errorf("as director returned %d works, want only %q", len(asDirector), "Habf")
	}

	everything, err := queries.ListWorksCreditedTo(t.Context(),
		librarydb.ListWorksCreditedToParams{PersonID: person.ID, Role: ""})
	if err != nil {
		t.Fatalf("ListWorksCreditedTo: %v", err)
	}
	if len(everything) != 2 {
		t.Errorf("unscoped returned %d works, want both", len(everything))
	}
}

// Aliases are load-bearing: a stage name is often the only thing connecting a
// release to a person (ADR-32).
func TestAliasesResolveToThePerson(t *testing.T) {
	opened, queries := newLibrary(t)
	person := createPerson(t, opened, "Nicholas Edward Cave")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.AddPersonAlias(ctx, librarydb.AddPersonAliasParams{
			PersonID: person.ID, Alias: "Nick Cave", Normalized: "nick cave", Source: "musicbrainz",
		})
	})

	found, err := queries.FindPeopleByAlias(t.Context(), "nick cave")
	if err != nil {
		t.Fatalf("FindPeopleByAlias: %v", err)
	}
	if len(found) != 1 || found[0].ID != person.ID {
		t.Errorf("the alias resolved to %d people, want person %d", len(found), person.ID)
	}
}

// "Monitor this label and everything beneath it" is the point of the hierarchy:
// sub-labels and imprints are real in every domain (ADR-32).
func TestOrganisationSubtreeIncludesDescendants(t *testing.T) {
	opened, queries := newLibrary(t)
	parent := createOrg(t, opened, "Warp Records", 0)
	child := createOrg(t, opened, "Arcola", parent.ID)
	grandchild := createOrg(t, opened, "Arcola Sub", child.ID)
	unrelated := createOrg(t, opened, "Ninja Tune", 0)

	subtree, err := queries.ListOrganisationSubtree(t.Context(), parent.ID)
	if err != nil {
		t.Fatalf("ListOrganisationSubtree: %v", err)
	}
	if len(subtree) != 3 {
		t.Fatalf("subtree has %d organisations, want 3", len(subtree))
	}
	for _, org := range subtree {
		if org.ID == unrelated.ID {
			t.Error("an unrelated label was included in the subtree")
		}
	}
	found := map[int64]bool{}
	for _, org := range subtree {
		found[org.ID] = true
	}
	if !found[grandchild.ID] {
		t.Error("the subtree stopped before the grandchild")
	}
}

// ADR-33: "this tag or any descendant" must be a join, not a recursive query per
// call, which is why the closure is materialised.
func TestTagClosureMatchesDescendants(t *testing.T) {
	opened, queries := newLibrary(t)
	electronic := createTag(t, opened, "electronic", 0)
	ambient := createTag(t, opened, "ambient", electronic.ID)
	work := createWork(t, opened, "music", "Selected Ambient Works")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.AssignTag(ctx, librarydb.AssignTagParams{
			TagID: ambient.ID, SubjectType: "work", SubjectID: work.ID, Source: "user",
		})
	})

	// Tagged ambient, found by searching the parent.
	subjects, err := queries.ListSubjectsForTagTree(t.Context(),
		librarydb.ListSubjectsForTagTreeParams{
			TagID: electronic.ID, SubjectType: "work", PageSize: 10,
		})
	if err != nil {
		t.Fatalf("ListSubjectsForTagTree: %v", err)
	}
	if len(subjects) != 1 || subjects[0].SubjectID != work.ID {
		t.Errorf("the parent tag matched %d subjects, want the work tagged with its child", len(subjects))
	}
}

// ADR-33 is explicit: a provider refresh never removes a user's assignment.
func TestAProviderRefreshLeavesUserTagsAlone(t *testing.T) {
	opened, queries := newLibrary(t)
	tag := createTag(t, opened, "live-recording", 0)
	work := createWork(t, opened, "music", "Live Seeds")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		for _, source := range []string{"user", "provider:musicbrainz"} {
			if err := q.AssignTag(ctx, librarydb.AssignTagParams{
				TagID: tag.ID, SubjectType: "work", SubjectID: work.ID, Source: source,
			}); err != nil {
				return err
			}
		}
		return nil
	})

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.ClearProviderTagAssignments(ctx, librarydb.ClearProviderTagAssignmentsParams{
			SubjectType: "work", SubjectID: work.ID, Source: "provider:musicbrainz",
		})
	})

	remaining, err := queries.ListTagsForSubject(t.Context(),
		librarydb.ListTagsForSubjectParams{SubjectType: "work", SubjectID: work.ID})
	if err != nil {
		t.Fatalf("ListTagsForSubject: %v", err)
	}
	if len(remaining) != 1 || remaining[0].Source != "user" {
		t.Errorf("a refresh removed the user's own tag: %+v", remaining)
	}
}

// A person and a provider both naming the same entity must reconcile to one row
// (ADR-8), or a subscription follows half a filmography.
func TestPersonExternalIDResolves(t *testing.T) {
	opened, queries := newLibrary(t)
	person := createPerson(t, opened, "Nick Cave")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.LinkPersonExternalID(ctx, librarydb.LinkPersonExternalIDParams{
			Provider: "musicbrainz", ExternalID: "artist-1", PersonID: person.ID,
		})
	})

	found, err := queries.GetPersonByExternalID(t.Context(),
		librarydb.GetPersonByExternalIDParams{Provider: "musicbrainz", ExternalID: "artist-1"})
	if err != nil {
		t.Fatalf("GetPersonByExternalID: %v", err)
	}
	if found.ID != person.ID {
		t.Errorf("resolved to %d, want %d", found.ID, person.ID)
	}
}

// One subscription per subject per user, or a double-click follows a person twice.
func TestSubscriptionsAreUniquePerSubjectAndUser(t *testing.T) {
	opened, _ := newLibrary(t)
	person := createPerson(t, opened, "Nick Cave")

	subscribe := func() error {
		return opened.Write(t.Context(), store.PrioInteractive,
			func(ctx context.Context, tx *sql.Tx) error {
				q := librarydb.New(opened.Read()).WithTx(tx)
				_, err := q.CreateSubscription(ctx, librarydb.CreateSubscriptionParams{
					SubjectType: "person", SubjectID: person.ID,
					MediaTypesJson: `["music"]`, Enabled: 1,
					UserID: sql.NullInt64{Int64: 1, Valid: true}, CreatedAt: 1,
				})
				return err
			})
	}
	if err := subscribe(); err != nil {
		t.Fatalf("first subscription: %v", err)
	}
	if err := subscribe(); err == nil {
		t.Error("the same person was subscribed to twice")
	}
}

// Removing a person takes their credits and aliases with them.
func TestDeletingAPersonCascades(t *testing.T) {
	opened, queries := newLibrary(t)
	person := createPerson(t, opened, "Nick Cave")
	work := createWork(t, opened, "music", "Tender Prey")

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		if err := q.AddCredit(ctx, librarydb.AddCreditParams{
			PersonID: person.ID, WorkID: work.ID, Role: "performer",
		}); err != nil {
			return err
		}
		return q.AddPersonAlias(ctx, librarydb.AddPersonAliasParams{
			PersonID: person.ID, Alias: "Nick", Normalized: "nick", Source: "user",
		})
	})

	write(t, opened, func(ctx context.Context, q *librarydb.Queries) error {
		return q.DeletePerson(ctx, person.ID)
	})

	credits, err := queries.ListCreditsForWork(t.Context(), work.ID)
	if err != nil {
		t.Fatalf("ListCreditsForWork: %v", err)
	}
	if len(credits) != 0 {
		t.Errorf("%d credits survived the person", len(credits))
	}
	aliases, err := queries.FindPeopleByAlias(t.Context(), "nick")
	if err != nil {
		t.Fatalf("FindPeopleByAlias: %v", err)
	}
	if len(aliases) != 0 {
		t.Errorf("%d aliases survived the person", len(aliases))
	}
}
