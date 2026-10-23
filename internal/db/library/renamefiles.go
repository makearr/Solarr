package library

import (
	"context"
	"fmt"
	"slices"

	"github.com/makearr/Solarr/internal/db/store/gen/librarydb"
)

// RenameFile is one file a rename may move: where it is, and which work and parts of the library it covers.
type RenameFile struct {
	FileOnRoot
	// Works are the works the file covers in the library, each with its parts; more than one is a file two works share.
	Works []RenameWork
}

// RenameWork is one work a file covers, and the parts of it the file holds (one episode, two of a double, one track).
type RenameWork struct {
	WorkID    int64
	EditionID int64
	MediaRefs []string
}

// RenameFilesOnRoot answers every primary file still on the root that the library's parts cover, workID narrowing to
// one work's files (0 for all): the one listing a rename of any media type starts from.
func (p Physical) RenameFilesOnRoot(ctx context.Context, categoryID, rootFolderID, workID int64) ([]RenameFile, error) {
	var files []RenameFile
	afterID := int64(0)
	for {
		rows, err := librarydb.New(p.store.Read()).ListRenameFilesOnRoot(ctx, librarydb.ListRenameFilesOnRootParams{
			CategoryID: categoryID, RootFolderID: rootFolderID, AfterID: afterID, WorkID: workID, PageSize: drainPageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("library: rename files on root %d: %w", rootFolderID, err)
		}
		pageObjects := 0
		for _, row := range rows {
			if len(files) == 0 || files[len(files)-1].MediaObjectID != row.MediaObjectID {
				files = append(files, RenameFile{FileOnRoot: FileOnRoot{MediaObjectID: row.MediaObjectID, RelPath: row.RelPath, SizeBytes: row.SizeBytes}})
				pageObjects++
			}
			file := &files[len(files)-1]
			index := slices.IndexFunc(file.Works, func(work RenameWork) bool { return work.WorkID == row.WorkID })
			if index < 0 {
				file.Works = append(file.Works, RenameWork{WorkID: row.WorkID, EditionID: row.EditionID})
				index = len(file.Works) - 1
			}
			if !slices.Contains(file.Works[index].MediaRefs, row.MediaRef) {
				file.Works[index].MediaRefs = append(file.Works[index].MediaRefs, row.MediaRef)
			}
			afterID = row.MediaObjectID
		}
		if pageObjects < drainPageSize {
			return files, nil
		}
	}
}
