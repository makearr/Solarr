package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/Solarr/internal/db/settings"
	"github.com/makearr/Solarr/internal/platform/config"
)

func TestCreateDownloadFolder_RoundTrips(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{
		Path: "/downloads/sabnzbd", Label: "sabnzbd", Readable: true,
	})
	if err != nil {
		t.Fatalf("CreateDownloadFolder: %v", err)
	}
	if created.ID == 0 || created.Path != "/downloads/sabnzbd" || !created.Readable {
		t.Errorf("created = %+v", created)
	}

	folders, err := cache.DownloadFolders(t.Context())
	if err != nil {
		t.Fatalf("DownloadFolders: %v", err)
	}
	if len(folders) != 1 || folders[0].ID != created.ID {
		t.Errorf("DownloadFolders = %+v, want exactly the one created", folders)
	}
}

func TestCreateDownloadFolder_RecordsAnUnreadablePath(t *testing.T) {
	cache, _ := newCache(t)

	created, err := cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{
		Path: "/downloads/broken", Readable: false, ReadableReason: "permission denied",
	})
	if err != nil {
		t.Fatalf("CreateDownloadFolder: %v", err)
	}
	if created.Readable || created.ReadableReason != "permission denied" {
		t.Errorf("created = %+v, want Readable=false with the probe's reason", created)
	}
}

func TestCreateDownloadFolder_AcceptsACategoryHint(t *testing.T) {
	cache, _ := newCache(t)
	category, err := cache.CreateCategory(t.Context(), "music", "Music", 100)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	created, err := cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{
		Path: "/downloads/music", CategoryID: &category.ID, Readable: true,
	})
	if err != nil {
		t.Fatalf("CreateDownloadFolder: %v", err)
	}
	if created.CategoryID == nil || *created.CategoryID != category.ID {
		t.Errorf("CategoryID = %v, want %d", created.CategoryID, category.ID)
	}
}

func TestCreateDownloadFolder_RejectsAnUnknownCategory(t *testing.T) {
	cache, _ := newCache(t)
	unknown := int64(999)

	_, err := cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{
		Path: "/downloads/music", CategoryID: &unknown, Readable: true,
	})
	if !errors.Is(err, settings.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestCreateDownloadFolder_RejectsEmptyPath(t *testing.T) {
	cache, _ := newCache(t)

	_, err := cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{Readable: true})
	if err == nil {
		t.Fatal("err = nil, want a validation error for an empty path")
	}
}

func TestDeleteDownloadFolder_RemovesIt(t *testing.T) {
	cache, _ := newCache(t)
	created, err := cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{
		Path: "/downloads/sabnzbd", Readable: true,
	})
	if err != nil {
		t.Fatalf("CreateDownloadFolder: %v", err)
	}

	if err := cache.DeleteDownloadFolder(t.Context(), created.ID); err != nil {
		t.Fatalf("DeleteDownloadFolder: %v", err)
	}
	folders, err := cache.DownloadFolders(t.Context())
	if err != nil {
		t.Fatalf("DownloadFolders: %v", err)
	}
	if len(folders) != 0 {
		t.Errorf("DownloadFolders = %+v, want none after delete", folders)
	}
}

func TestGetDownloadFolder_MissingIDAnswersNoRows(t *testing.T) {
	cache, _ := newCache(t)

	if _, err := cache.GetDownloadFolder(t.Context(), 999); err == nil {
		t.Fatal("err = nil, want an error for an unknown id")
	}
}

// A watched folder's downloads are imported into the library it names, so watching without one is refused, on
// creating and on changing a folder alike; nothing is guessed.
func TestDownloadFolder_WatchingNeedsALibrary(t *testing.T) {
	cache, _ := newCache(t)
	library, err := cache.CreateCategory(t.Context(), "music", "Music", 10)
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}

	_, err = cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{Path: "/downloads/a", Readable: true, Watch: true})
	var problems config.Problems
	if !errors.As(err, &problems) || len(problems) != 1 || problems[0].Path != "category_id" {
		t.Fatalf("creating a watched folder without a library = %v, want one problem on category_id", err)
	}

	unwatched, err := cache.CreateDownloadFolder(t.Context(), settings.DownloadFolderRequest{Path: "/downloads/b", Readable: true})
	if err != nil {
		t.Fatalf("CreateDownloadFolder: %v", err)
	}
	if _, err := cache.UpdateDownloadFolder(t.Context(), unwatched.ID, settings.DownloadFolderUpdate{Watch: true}); !errors.As(err, &problems) {
		t.Errorf("watching an existing folder without a library = %v, want it refused", err)
	}
	watched, err := cache.UpdateDownloadFolder(t.Context(), unwatched.ID, settings.DownloadFolderUpdate{Watch: true, CategoryID: &library.ID})
	if err != nil || !watched.Watch || watched.CategoryID == nil || *watched.CategoryID != library.ID {
		t.Errorf("watching with a library = %+v, %v, want it watched for library %d", watched, err, library.ID)
	}
}
