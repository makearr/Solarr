package library

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/makearr/Solarr/internal/db/store"
)

// Watch statuses of a work for a viewer.
const (
	WatchedYes        = "yes"
	WatchedNo         = "no"
	WatchedInProgress = "in_progress"
)

// PlaybackPair names one media server user whose state counts for a viewer.
type PlaybackPair struct {
	ServerID     int64
	ServerUserID string
}

// PairsJSON encodes pairs for the SQL that tests membership with json_each.
func PairsJSON(pairs []PlaybackPair) string {
	type one struct {
		S int64  `json:"s"`
		U string `json:"u"`
	}
	encoded := make([]one, len(pairs))
	for i, pair := range pairs {
		encoded[i] = one{S: pair.ServerID, U: pair.ServerUserID}
	}
	out, _ := json.Marshal(encoded)
	return string(out)
}

// finishedBy is true where the pair named by the json_each row alias j has played all of work: the whole-work row, or every part.
func finishedBy(work string) string {
	return "EXISTS (SELECT 1 FROM playback_state p WHERE p.work_id = " + work + " AND p.server_id = json_extract(j.value, '$.s')" +
		" AND p.server_user_id = json_extract(j.value, '$.u') AND p.played = 1 AND (p.part_id = 0 OR" +
		" (SELECT COUNT(*) FROM playback_state q WHERE q.work_id = p.work_id AND q.server_id = p.server_id AND q.server_user_id = p.server_user_id" +
		" AND q.part_id <> 0 AND q.played = 1) >= (SELECT COUNT(*) FROM work_part wp WHERE wp.work_id = p.work_id)))"
}

const startedBy = "EXISTS (SELECT 1 FROM playback_state p, json_each(?) j WHERE p.work_id = work.id AND p.server_id = json_extract(j.value, '$.s')" +
	" AND p.server_user_id = json_extract(j.value, '$.u') AND (p.played = 1 OR p.position_ms > 0))"

// addWatched narrows a work list to one watch status as seen by the pairs; "" adds nothing.
func addWatched(predicates *store.Predicates, status string, pairs []PlaybackPair) {
	encoded := PairsJSON(pairs)
	finished := "EXISTS (SELECT 1 FROM json_each(?) j WHERE " + finishedBy("work.id") + ")"
	switch status {
	case WatchedYes:
		predicates.Add(finished, encoded)
	case WatchedNo:
		predicates.Add("NOT "+startedBy, encoded)
	case WatchedInProgress:
		predicates.Add(startedBy, encoded)
		predicates.Add("NOT "+finished, encoded)
	}
}

// WorkWatch is what a set of server users has played of one work.
type WorkWatch struct {
	// Finished and Started count the users who played all of it, and who played or began some of it.
	Finished, Started int
	LastPlayedAt      int64
}

type playbackRow struct {
	workID, serverID, partID, positionMS, durationMS, lastPlayed, partTotal int64
	user                                                                    string
}

// WatchOf answers the watch state of the works for the pairs, one read for a page of the list.
func (p Playback) WatchOf(ctx context.Context, pairs []PlaybackPair, workIDs []int64) (map[int64]WorkWatch, error) {
	out := map[int64]WorkWatch{}
	if len(pairs) == 0 || len(workIDs) == 0 {
		return out, nil
	}
	ids, err := json.Marshal(workIDs)
	if err != nil {
		return nil, err
	}
	rows, err := p.store.Read().QueryContext(ctx,
		"SELECT p.work_id, p.server_id, p.server_user_id, p.part_id, p.played, p.position_ms, p.duration_ms, COALESCE(p.last_played_at, 0),"+
			" CAST((SELECT COUNT(*) FROM work_part wp WHERE wp.work_id = p.work_id) AS INTEGER)"+
			" FROM playback_state p WHERE p.work_id IN (SELECT value FROM json_each(?))"+
			" AND EXISTS (SELECT 1 FROM json_each(?) j WHERE json_extract(j.value, '$.s') = p.server_id AND json_extract(j.value, '$.u') = p.server_user_id)",
		string(ids), PairsJSON(pairs))
	if err != nil {
		return nil, fmt.Errorf("library: watch state of works: %w", err)
	}
	defer func() { _ = rows.Close() }()
	type key struct {
		work, server int64
		user         string
	}
	type tally struct {
		whole, started bool
		parts, total   int64
		last           int64
	}
	tallies := map[key]*tally{}
	for rows.Next() {
		var row playbackRow
		var played int64
		if err := rows.Scan(&row.workID, &row.serverID, &row.user, &row.partID, &played, &row.positionMS, &row.durationMS, &row.lastPlayed, &row.partTotal); err != nil {
			return nil, err
		}
		k := key{row.workID, row.serverID, row.user}
		t := tallies[k]
		if t == nil {
			t = &tally{total: row.partTotal}
			tallies[k] = t
		}
		if played == 1 {
			t.started = true
			if row.partID == 0 {
				t.whole = true
			} else {
				t.parts++
			}
		} else if row.positionMS > 0 {
			t.started = true
		}
		t.last = max(t.last, row.lastPlayed)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for k, t := range tallies {
		watch := out[k.work]
		if t.started {
			watch.Started++
		}
		if t.whole || t.total > 0 && t.parts >= t.total {
			watch.Finished++
		}
		watch.LastPlayedAt = max(watch.LastPlayedAt, t.last)
		out[k.work] = watch
	}
	return out, rows.Err()
}

// PartPlay is one user's state of one part of a work (PartID 0 is the whole work).
type PartPlay struct {
	PartID       int64
	MediaRef     string
	Title        string
	ServerID     int64
	ServerUserID string
	Played       bool
	PositionMS   int64
	DurationMS   int64
	LastPlayedAt int64
}

// PartsOf lists the stored state of one work for the pairs, whole-work rows first, then parts in order.
func (p Playback) PartsOf(ctx context.Context, pairs []PlaybackPair, workID int64) ([]PartPlay, error) {
	if len(pairs) == 0 {
		return nil, nil
	}
	rows, err := p.store.Read().QueryContext(ctx,
		"SELECT p.part_id, COALESCE(wp.media_ref, ''), COALESCE(wp.title, ''), p.server_id, p.server_user_id, p.played, p.position_ms, p.duration_ms,"+
			" COALESCE(p.last_played_at, 0) FROM playback_state p LEFT JOIN work_part wp ON wp.id = p.part_id"+
			" WHERE p.work_id = ? AND EXISTS (SELECT 1 FROM json_each(?) j WHERE json_extract(j.value, '$.s') = p.server_id AND json_extract(j.value, '$.u') = p.server_user_id)"+
			" ORDER BY p.part_id <> 0, COALESCE(wp.sort_key, ''), p.part_id, p.server_id, p.server_user_id",
		workID, PairsJSON(pairs))
	if err != nil {
		return nil, fmt.Errorf("library: watch state of work %d: %w", workID, err)
	}
	defer func() { _ = rows.Close() }()
	var out []PartPlay
	for rows.Next() {
		var row PartPlay
		var played int64
		if err := rows.Scan(&row.PartID, &row.MediaRef, &row.Title, &row.ServerID, &row.ServerUserID, &played, &row.PositionMS, &row.DurationMS, &row.LastPlayedAt); err != nil {
			return nil, err
		}
		row.Played = played == 1
		out = append(out, row)
	}
	return out, rows.Err()
}

// WatchedWork is a work every given user has played, and that still has files.
type WatchedWork struct {
	ID           int64
	Title        string
	Year         int64
	LastPlayedAt int64
	SizeBytes    int64
	FileCount    int64
}

// WatchedQuery selects the cleanup list: works all the pairs finished, last played at or before the cutoff.
type WatchedQuery struct {
	RootFolderIDs, CategoryIDs []int64
	MediaTypes                 []string
	Pairs                      []PlaybackPair
	LastPlayedBefore           int64
	// After is the id the previous page ended on; ids ascend.
	After int64
	Limit int
	// IDs restricts to these works, for the consequences of a selection.
	IDs []int64
}

// WatchedWorks lists the cleanup list in work id order.
func (p Playback) WatchedWorks(ctx context.Context, query WatchedQuery) ([]WatchedWork, error) {
	if len(query.Pairs) == 0 {
		return nil, nil
	}
	encoded := PairsJSON(query.Pairs)
	var predicates store.Predicates
	predicates.Add("work.id > ?", query.After)
	if query.MediaTypes != nil {
		fragment, arguments, _ := membershipStrings("media_type IN (%s)", query.MediaTypes)
		predicates.Add(fragment, arguments...)
	}
	addVisibility(&predicates, query.RootFolderIDs, query.CategoryIDs)
	if query.IDs != nil {
		ids, _ := json.Marshal(query.IDs)
		predicates.Add("work.id IN (SELECT value FROM json_each(?))", string(ids))
	}
	predicates.Add("EXISTS (SELECT 1 FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id WHERE wf.work_id = work.id AND lp.missing_since IS NULL)")
	predicates.Add("(SELECT COUNT(*) FROM json_each(?) j WHERE "+finishedBy("work.id")+") = ?", encoded, len(query.Pairs))
	predicates.Add("COALESCE((SELECT MAX(p.last_played_at) FROM playback_state p WHERE p.work_id = work.id AND EXISTS"+
		" (SELECT 1 FROM json_each(?) j WHERE json_extract(j.value, '$.s') = p.server_id AND json_extract(j.value, '$.u') = p.server_user_id)), 0) <= ?",
		encoded, query.LastPlayedBefore)
	limit := query.Limit
	if limit <= 0 {
		limit = 50
	}
	sqlText := "SELECT work.id, work.canonical_title, COALESCE(work.year, 0)," +
		" CAST(COALESCE((SELECT MAX(p.last_played_at) FROM playback_state p WHERE p.work_id = work.id), 0) AS INTEGER)," +
		" CAST(COALESCE((SELECT SUM(mo.size_bytes) FROM work_file wf JOIN media_object mo ON mo.id = wf.media_object_id WHERE wf.work_id = work.id), 0) AS INTEGER)," +
		" CAST((SELECT COUNT(*) FROM work_file wf WHERE wf.work_id = work.id) AS INTEGER)" +
		" FROM work " + predicates.Where() + " ORDER BY work.id LIMIT " + fmt.Sprint(limit)
	rows, err := p.store.Read().QueryContext(ctx, sqlText, predicates.Args()...)
	if err != nil {
		return nil, fmt.Errorf("library: watched works: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []WatchedWork
	for rows.Next() {
		var row WatchedWork
		if err := rows.Scan(&row.ID, &row.Title, &row.Year, &row.LastPlayedAt, &row.SizeBytes, &row.FileCount); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// WorkFileIDs answers the media object ids of a work's files that still exist.
func (p Playback) WorkFileIDs(ctx context.Context, workID int64) ([]int64, error) {
	rows, err := p.store.Read().QueryContext(ctx,
		"SELECT DISTINCT wf.media_object_id FROM work_file wf JOIN library_path lp ON lp.media_object_id = wf.media_object_id"+
			" WHERE wf.work_id = ? AND lp.missing_since IS NULL ORDER BY wf.media_object_id", workID)
	if err != nil {
		return nil, fmt.Errorf("library: files of work %d: %w", workID, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
