package library_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/makearr/Solarr/internal/db/library"
	"github.com/makearr/Solarr/internal/db/store"
)

func TestDownloadedFiles_AreThoseAnAcquisitionOrAManualImportBrought(t *testing.T) {
	opened, _ := newLibrary(t)
	work := createWork(t, opened, "music", "An Album")
	origins := []struct {
		origin      string
		acquisition any
	}{{"acquisition", 7}, {"manual", nil}, {"adoption", nil}, {"watched", nil}}
	objects := make([]int64, len(origins))
	for index, entry := range origins {
		objects[index] = createObject(t, opened, fmt.Sprintf("hash-%d", index), 10).ID
		err := opened.Write(t.Context(), store.PrioInteractive, func(ctx context.Context, tx *sql.Tx) error {
			return placeBy(ctx, tx, work.ID, objects[index], entry.origin, entry.acquisition)
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	downloaded, err := library.NewDesired(opened).DownloadedFiles(t.Context(), work.ID)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []bool{true, true, false, false} {
		if downloaded[objects[index]] != want {
			t.Errorf("origin %s: downloaded = %v, want %v", origins[index].origin, downloaded[objects[index]], want)
		}
	}
}

func placeBy(ctx context.Context, tx *sql.Tx, workID, objectID int64, origin string, acquisition any) error {
	steps := []struct {
		query string
		args  []any
	}{
		{"INSERT INTO work_file (work_id, media_object_id, first_seen_at) VALUES (?, ?, 1)", []any{workID, objectID}},
		{"INSERT INTO import_session (source_path, origin, acquisition_id, state, threshold, started_at) VALUES ('/src', ?, ?, 'DONE', 0.9, 1)", []any{origin, acquisition}},
		{"INSERT INTO import_unit (session_id, rel_path, shape, file_count, bytes, confidence, decision, evidence_json) VALUES ((SELECT MAX(id) FROM import_session), 'a', 'SET', 1, 10, 1.0, 'COMMIT', '{}')", nil},
		{"INSERT INTO import_unit_file (unit_id, media_object_id) VALUES ((SELECT MAX(id) FROM import_unit), ?)", []any{objectID}},
	}
	for _, step := range steps {
		if _, err := tx.ExecContext(ctx, step.query, step.args...); err != nil {
			return err
		}
	}
	return nil
}
