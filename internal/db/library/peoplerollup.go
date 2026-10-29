package library

import (
	"context"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// rollupChunk bounds the ids one refresh statement names.
const rollupChunk = 400

// rollupMembers are the people and organisations whose library rows a work's change can move.
type rollupMembers struct {
	people, organisations []int64
}

// membersOfWork reads them before a change that would hide them, such as the work's deletion.
func membersOfWork(ctx context.Context, queries *librarydb.Queries, workID int64) (rollupMembers, error) {
	people, err := queries.ListPeopleOfWork(ctx, workID)
	if err != nil {
		return rollupMembers{}, fmt.Errorf("people of work %d: %w", workID, err)
	}
	organisations, err := queries.ListOrganisationsOfWork(ctx, workID)
	if err != nil {
		return rollupMembers{}, fmt.Errorf("organisations of work %d: %w", workID, err)
	}
	return rollupMembers{people: people, organisations: organisations}, nil
}

// refresh recomputes the library rows of every member, in the caller's transaction.
func (m rollupMembers) refresh(ctx context.Context, queries *librarydb.Queries) error {
	if err := refreshPeople(ctx, queries, m.people); err != nil {
		return err
	}
	return refreshOrganisations(ctx, queries, m.organisations)
}

// refreshWorkMembers recomputes the rows of everyone credited on, or organisation linked to, the work.
func refreshWorkMembers(ctx context.Context, queries *librarydb.Queries, workID int64) error {
	members, err := membersOfWork(ctx, queries, workID)
	if err != nil {
		return err
	}
	return members.refresh(ctx, queries)
}

// LibrariesOf answers the libraries holding any of a person's works, by id.
func (p People) LibrariesOf(ctx context.Context, personID int64) ([]int64, error) {
	categoryIDs, err := librarydb.New(p.store.Read()).ListPersonLibraryCategories(ctx, personID)
	if err != nil {
		return nil, fmt.Errorf("library: libraries of person %d: %w", personID, err)
	}
	return categoryIDs, nil
}

// refreshPeople recomputes person_library for the given people from their credits and their works' libraries.
func refreshPeople(ctx context.Context, queries *librarydb.Queries, personIDs []int64) error {
	for chunk := range slices.Chunk(unique(personIDs), rollupChunk) {
		if err := queries.ClearPersonLibrary(ctx, chunk); err != nil {
			return fmt.Errorf("clear person libraries: %w", err)
		}
		if err := queries.FillPersonLibrary(ctx, chunk); err != nil {
			return fmt.Errorf("fill person libraries: %w", err)
		}
	}
	return nil
}

// refreshOrganisations is refreshPeople for organisation_library.
func refreshOrganisations(ctx context.Context, queries *librarydb.Queries, organisationIDs []int64) error {
	for chunk := range slices.Chunk(unique(organisationIDs), rollupChunk) {
		if err := queries.ClearOrganisationLibrary(ctx, chunk); err != nil {
			return fmt.Errorf("clear organisation libraries: %w", err)
		}
		if err := queries.FillOrganisationLibrary(ctx, chunk); err != nil {
			return fmt.Errorf("fill organisation libraries: %w", err)
		}
	}
	return nil
}

// enteredCategory refreshes a work's members when its first wanted item of a library was just created.
func enteredCategory(ctx context.Context, queries *librarydb.Queries, workID, categoryID int64) error {
	items, err := queries.CountDesiredItemsInCategory(ctx, librarydb.CountDesiredItemsInCategoryParams{WorkID: workID, CategoryID: categoryID})
	if err != nil {
		return fmt.Errorf("items of work %d in library %d: %w", workID, categoryID, err)
	}
	if items != 1 {
		return nil
	}
	return refreshWorkMembers(ctx, queries, workID)
}

// createDesiredItem is the one writer of a wanted item, so a work entering a library brings its people with it.
func createDesiredItem(ctx context.Context, queries *librarydb.Queries, params librarydb.CreateDesiredItemParams) (librarydb.DesiredItem, error) {
	created, err := queries.CreateDesiredItem(ctx, params)
	if err != nil {
		return librarydb.DesiredItem{}, err
	}
	return created, enteredCategory(ctx, queries, params.WorkID, params.CategoryID)
}

func unique(ids []int64) []int64 {
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	return slices.Compact(sorted)
}
