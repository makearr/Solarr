package settings_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
)

func musicNaming(standard string) settings.NamingProfile {
	return settings.NamingProfile{
		RenameFiles: true, ReplaceIllegalCharacters: true, ColonReplacement: "smart",
		Formats: map[string]string{"artist_folder": "{Artist Name}", "standard_track": standard},
	}
}

// A library has no naming until it is saved; the first save creates its own profile and a
// second save replaces it rather than adding another.
func TestSetCategoryNaming_CreatesThenReplaces(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if got, _ := cache.Snapshot().Category(category.ID); got.Naming != nil {
		t.Fatalf("a new library has naming %+v, want none", got.Naming)
	}

	first := musicNaming("{track:00} - {Track Title}")
	if err := cache.SetCategoryNaming(t.Context(), category.ID, first); err != nil {
		t.Fatalf("SetCategoryNaming: %v", err)
	}
	second := settings.NamingProfile{
		RenameFiles: false, ReplaceIllegalCharacters: false, ColonReplacement: "delete",
		Formats: map[string]string{"standard_track": "{Track Title}"},
	}
	if err := cache.SetCategoryNaming(t.Context(), category.ID, second); err != nil {
		t.Fatalf("SetCategoryNaming again: %v", err)
	}

	got, _ := cache.Snapshot().Category(category.ID)
	if got.Naming == nil || !reflect.DeepEqual(*got.Naming, second) {
		t.Errorf("naming = %+v, want %+v", got.Naming, second)
	}
}

func TestSetCategoryNaming_KeepsEachLibraryItsOwn(t *testing.T) {
	cache, _ := newCache(t)
	music, _ := cache.CreateCategory(t.Context(), "music", "Music", 10)
	soundtracks, _ := cache.CreateCategory(t.Context(), "music", "Soundtracks", 20)

	if err := cache.SetCategoryNaming(t.Context(), music.ID, musicNaming("{Track Title}")); err != nil {
		t.Fatal(err)
	}
	if err := cache.SetCategoryNaming(t.Context(), soundtracks.ID, musicNaming("{track:00}")); err != nil {
		t.Fatal(err)
	}
	a, _ := cache.Snapshot().Category(music.ID)
	b, _ := cache.Snapshot().Category(soundtracks.ID)
	if a.Naming.Formats["standard_track"] != "{Track Title}" || b.Naming.Formats["standard_track"] != "{track:00}" {
		t.Errorf("formats crossed: %v / %v", a.Naming.Formats, b.Naming.Formats)
	}
}

// The media type owns the vocabulary; a refusal must stop the write before anything is stored.
func TestSetCategoryNaming_RefusedByTheValidator(t *testing.T) {
	cache, _ := newCache(t)
	category, _ := cache.CreateCategory(t.Context(), "music", "Music", 10)
	refusal := errors.New("unknown token: Episode Title")
	var sawMediaType string
	cache.EnableNamingValidation(func(mediaType string, _ settings.NamingProfile) error {
		sawMediaType = mediaType
		return refusal
	})

	err := cache.SetCategoryNaming(t.Context(), category.ID, musicNaming("{Episode Title}"))
	if !errors.Is(err, refusal) || !errors.Is(err, settings.ErrInvalidNaming) {
		t.Fatalf("err = %v, want the validator's refusal as ErrInvalidNaming", err)
	}
	if sawMediaType != "music" {
		t.Errorf("validator saw media type %q, want music", sawMediaType)
	}
	if got, _ := cache.Snapshot().Category(category.ID); got.Naming != nil {
		t.Errorf("a refused save stored %+v", got.Naming)
	}
}

func TestDeleteCategory_TakesItsNamingProfileWithIt(t *testing.T) {
	cache, _ := newCache(t)
	category, _ := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err := cache.SetCategoryNaming(t.Context(), category.ID, musicNaming("{Track Title}")); err != nil {
		t.Fatal(err)
	}
	if err := cache.DeleteCategory(t.Context(), category.ID); err != nil {
		t.Fatalf("DeleteCategory: %v", err)
	}
	// SQLite reuses the freed id, so a leftover "library-<id>" profile would collide on UNIQUE.
	again, _ := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if again.ID != category.ID {
		t.Fatalf("id %d was not reused (got %d), so this test proves nothing", category.ID, again.ID)
	}
	if err := cache.SetCategoryNaming(t.Context(), again.ID, musicNaming("{Track Title}")); err != nil {
		t.Fatalf("SetCategoryNaming after delete: %v", err)
	}
}
