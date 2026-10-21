-- A realign offered files already on a track to the album's unfilled tracks, so one music file came to cover two
-- (a "Mercy" also on "Mercy (acoustic)"). Keep the track the file's own claim names, else the one it was on first.
CREATE TEMP TABLE music_file_extra_track AS
SELECT f.desired_item_id, f.media_object_id, f.media_ref
FROM desired_item_file f
JOIN desired_item i ON i.id = f.desired_item_id
JOIN work w ON w.id = i.work_id AND w.media_type = 'music'
LEFT JOIN file_track_claim c ON c.media_object_id = f.media_object_id
WHERE f.role = 'primary'
  AND EXISTS (SELECT 1 FROM desired_item_file o
              WHERE o.desired_item_id = f.desired_item_id AND o.media_object_id = f.media_object_id
                AND o.role = 'primary' AND o.media_ref <> f.media_ref)
  AND CASE
        WHEN EXISTS (SELECT 1 FROM desired_item_file o
                     WHERE o.desired_item_id = f.desired_item_id AND o.media_object_id = f.media_object_id
                       AND o.role = 'primary' AND o.media_ref = 'disc' || c.disc || ':track' || c.track)
          THEN f.media_ref <> 'disc' || c.disc || ':track' || c.track
        ELSE EXISTS (SELECT 1 FROM desired_item_file o
                     WHERE o.desired_item_id = f.desired_item_id AND o.media_object_id = f.media_object_id
                       AND o.role = 'primary'
                       AND (o.added_at < f.added_at OR (o.added_at = f.added_at AND o.media_ref < f.media_ref)))
      END;

DELETE FROM desired_item_file
WHERE role = 'primary'
  AND EXISTS (SELECT 1 FROM music_file_extra_track x
              WHERE x.desired_item_id = desired_item_file.desired_item_id
                AND x.media_object_id = desired_item_file.media_object_id
                AND x.media_ref = desired_item_file.media_ref);

-- The same count and state the album recompute writes (RecomputePresentCount, albumState).
UPDATE desired_item
SET present_count = (
      SELECT COUNT(DISTINCT f.media_ref)
      FROM desired_item_file f
      WHERE f.desired_item_id = desired_item.id AND f.role = 'primary'
        AND NOT (EXISTS (SELECT 1 FROM library_path gone WHERE gone.media_object_id = f.media_object_id
                           AND gone.missing_since IS NOT NULL)
                 AND NOT EXISTS (SELECT 1 FROM library_path held WHERE held.media_object_id = f.media_object_id
                                   AND held.missing_since IS NULL))),
    dirty = 1
WHERE id IN (SELECT DISTINCT desired_item_id FROM music_file_extra_track);

UPDATE desired_item
SET state = CASE
              WHEN present_count <= 0 THEN 'MISSING'
              WHEN required_count IS NOT NULL AND present_count >= required_count THEN state
              ELSE 'PARTIAL'
            END
WHERE id IN (SELECT DISTINCT desired_item_id FROM music_file_extra_track);

DROP TABLE music_file_extra_track;
