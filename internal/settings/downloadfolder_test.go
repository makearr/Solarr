package settings_test

import (
	"errors"
	"testing"

	"github.com/makearr/MediaNexus/internal/settings"
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
