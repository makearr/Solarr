package recovery

import (
	"context"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
	"github.com/makearr/Solarr/internal/platform/clock"
)

// LostInRestore is the reason a download the restored library calls active, and its client no longer holds, is closed with.
const LostInRestore = "lost in a database restore: the download client no longer holds it"

// HeldDownload is one download a client holds.
type HeldDownload struct {
	Ref, Name, Category string
	// Path is where its files are; empty until it has finished.
	Path     string
	Finished bool
}

// DownloadClient is a download client as the restore needs it: what it holds under the given categories. It is an
// interface of this package's own, wired where the clients are built, so recovery imports none of them.
type DownloadClient struct {
	ID         int64
	Name       string
	Categories []string
	Held       func(ctx context.Context, categories []string) ([]HeldDownload, error)
}

// UntrackedDownload is a download a client holds under one of Solarr's categories that no acquisition references.
type UntrackedDownload struct {
	ClientID   int64  `json:"client_id"`
	Client     string `json:"client"`
	Name       string `json:"name"`
	Path       string `json:"path,omitempty"`
	Finished   bool   `json:"finished"`
	ClientPath string `json:"-"`
}

// reconcileDownloads closes the active downloads their client lost, and finds what a client holds that nothing
// references. A client that cannot be asked is a Gap: its downloads are left to the poll, which waits out a grace
// period before treating a missing one as removed.
func reconcileDownloads(
	ctx context.Context, cl clock.Clock, libraryStore *store.Store, clients []DownloadClient, report *Report,
) error {
	acquisitions := library.NewAcquisitions(libraryStore)
	active, err := acquisitions.ListActive(ctx)
	if err != nil {
		return err
	}
	referenced, err := acquisitions.ClientRefs(ctx)
	if err != nil {
		return err
	}
	for _, client := range clients {
		categories := slices.Clone(client.Categories)
		for _, acquisition := range active {
			if acquisition.DownloaderID == client.ID && acquisition.ClientCategory != "" && !slices.Contains(categories, acquisition.ClientCategory) {
				categories = append(categories, acquisition.ClientCategory)
			}
		}
		slices.Sort(categories)
		held, err := client.Held(ctx, categories)
		if err != nil {
			report.Unreached = append(report.Unreached, client.ID)
			report.Gaps = append(report.Gaps, Gap{
				What:    "re-derive in-flight downloads from the " + client.Name + " download client",
				Because: "the client could not be asked (" + err.Error() + "); the poll follows its downloads and waits before treating a missing one as removed",
			})
			continue
		}
		present := make(map[string]bool, len(held))
		for _, download := range held {
			present[download.Ref] = true
			if !referenced[client.ID][download.Ref] {
				report.Untracked = append(report.Untracked, UntrackedDownload{
					ClientID: client.ID, Client: client.Name, Name: download.Name, Path: download.Path, Finished: download.Finished,
				})
			}
		}
		for _, acquisition := range active {
			if acquisition.DownloaderID != client.ID || present[acquisition.DownloadClientRef] {
				continue
			}
			err := acquisitions.Transition(ctx, acquisition.ID, acquisition.State, "CANCELLED", LostInRestore, cl.Now().Unix())
			if err != nil {
				return fmt.Errorf("recovery: close download %d: %w", acquisition.ID, err)
			}
			report.DownloadsClosed++
		}
	}
	return nil
}

// ReconcileDownloads asks the clients again, as the restore did: it closes what they lost and finds what nothing
// references. The report names the clients that still could not be asked.
func ReconcileDownloads(ctx context.Context, cl clock.Clock, libraryStore *store.Store, clients []DownloadClient) (Report, error) {
	var report Report
	err := reconcileDownloads(ctx, cl, libraryStore, clients, &report)
	return report, err
}
