-- A movie is listed under its director, as an album is under its artist: the works list, its sort and the work page read it.
UPDATE work SET
  lead_person_id = (SELECT cr.person_id FROM credit cr WHERE cr.work_id = work.id AND cr.part_id = 0 AND cr.edition_id = 0 AND cr.role = 'director'
                    ORDER BY cr.ord, cr.person_id LIMIT 1),
  lead_person_sort_name = (SELECT pe.sort_name FROM credit cr JOIN person pe ON pe.id = cr.person_id
                           WHERE cr.work_id = work.id AND cr.part_id = 0 AND cr.edition_id = 0 AND cr.role = 'director'
                           ORDER BY cr.ord, cr.person_id LIMIT 1)
WHERE work.media_type = 'movie' AND work.lead_person_id IS NULL
  AND EXISTS (SELECT 1 FROM credit cr WHERE cr.work_id = work.id AND cr.part_id = 0 AND cr.edition_id = 0 AND cr.role = 'director');
