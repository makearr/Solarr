package library

import (
	"context"
	"fmt"
)

// ArchivePartFile is one file behind a part of an item of a type whose parts are archives of pages.
type ArchivePartFile struct {
	// Ref is the part's media ref ("issue:5").
	Ref           string
	MediaObjectID int64
	AddedAt       int64
	Monitored     bool
	SizeBytes     int64
	// Container is the archive's own format; ImageFormat the commonest page format; PageCount the pages.
	Container, ImageFormat string
	PageCount              int
	// Short and Long are the median short and long edge of the pages in pixels; 0 when the pages were not measured.
	Short, Long                 int
	ReleaseSource, ReleaseGroup string
	Revision                    int64
}

// ArchivePartFiles lists every file of every part of an item, the incumbent (lowest media object id) first.
func (r SeriesReader) ArchivePartFiles(ctx context.Context, itemID int64) ([]ArchivePartFile, error) {
	rows, err := r.store.Read().QueryContext(ctx,
		`SELECT f.media_ref, mo.id, f.added_at, p.monitored, mo.size_bytes, COALESCE(ma.container, ''), COALESCE(ma.image_format, ''),
       COALESCE(ma.page_count, 0), COALESCE(mo.width, 0), COALESCE(mo.height, 0),
       COALESCE(r.source, ''), COALESCE(r.release_group, ''), COALESCE(r.revision, 0)
FROM desired_item_file f
JOIN desired_item d ON d.id = f.desired_item_id
JOIN work_part p ON p.work_id = d.work_id AND p.media_ref = f.media_ref
JOIN media_object mo ON mo.id = f.media_object_id
LEFT JOIN media_attributes ma ON ma.id = mo.measured_attributes_id
LEFT JOIN media_object_release r ON r.media_object_id = mo.id
WHERE f.desired_item_id = ? AND f.role = 'primary' AND f.media_ref <> ''
ORDER BY f.media_ref, mo.id`, itemID)
	if err != nil {
		return nil, fmt.Errorf("library: part files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var files []ArchivePartFile
	for rows.Next() {
		var file ArchivePartFile
		var monitored int64
		if err := rows.Scan(&file.Ref, &file.MediaObjectID, &file.AddedAt, &monitored, &file.SizeBytes, &file.Container, &file.ImageFormat,
			&file.PageCount, &file.Short, &file.Long, &file.ReleaseSource, &file.ReleaseGroup, &file.Revision); err != nil {
			return nil, fmt.Errorf("library: scan part file: %w", err)
		}
		file.Monitored = monitored == 1
		files = append(files, file)
	}
	return files, rows.Err()
}
