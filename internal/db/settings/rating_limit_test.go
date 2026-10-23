package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/platform/config"
)

// A library's maximum rating is stored and read back; an unrated work is let in only when the library says so, and an
// age no rating names is refused.
func TestCategoryRatingLimit_AdmitsUpToItsAge(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "tv", "Kids", 1)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if !category.AdmitsAge(18) || !category.AdmitsAge(-1) {
		t.Fatal("a library without a limit admits everything")
	}
	thirteen := int64(13)
	limited, err := cache.SetCategoryRatingLimit(t.Context(), category.ID, &thirteen, false)
	if err != nil {
		t.Fatalf("SetCategoryRatingLimit: %v", err)
	}
	for age, want := range map[int64]bool{0: true, 13: true, 14: false, 17: false, -1: false} {
		if got := limited.AdmitsAge(age); got != want {
			t.Errorf("up to 13, age %d admitted = %v, want %v", age, got, want)
		}
	}
	if reread, _ := cache.Snapshot().Category(category.ID); reread.MaxAge == nil || *reread.MaxAge != 13 || reread.AllowUnrated {
		t.Errorf("read back = %+v, want up to 13 without unrated", reread)
	}
	unrated, err := cache.SetCategoryRatingLimit(t.Context(), category.ID, &thirteen, true)
	if err != nil || !unrated.AdmitsAge(-1) {
		t.Errorf("allow unrated: %v, admits unrated %v", err, unrated.AdmitsAge(-1))
	}
	tooOld := int64(30)
	var problems config.Problems
	if _, err := cache.SetCategoryRatingLimit(t.Context(), category.ID, &tooOld, false); !errors.As(err, &problems) {
		t.Errorf("age 30: %v, want problems", err)
	}
	cleared, err := cache.SetCategoryRatingLimit(t.Context(), category.ID, nil, false)
	if err != nil || cleared.MaxAge != nil || !cleared.AdmitsAge(18) {
		t.Errorf("cleared = %+v, %v; want no limit", cleared, err)
	}
}
