-- Rows that name a deleted work or person by (subject_type, subject_id) have no foreign key, so nothing removed them.

DELETE FROM tag_assignment WHERE subject_type = 'work' AND subject_id NOT IN (SELECT id FROM work);
DELETE FROM favorite WHERE subject_type = 'work' AND subject_id NOT IN (SELECT id FROM work);
DELETE FROM artwork WHERE subject_type = 'work' AND subject_id NOT IN (SELECT id FROM work);
DELETE FROM artwork_miss WHERE subject_type = 'work' AND subject_id NOT IN (SELECT id FROM work);
DELETE FROM tag_assignment WHERE subject_type = 'person' AND subject_id NOT IN (SELECT id FROM person);
DELETE FROM favorite WHERE subject_type = 'person' AND subject_id NOT IN (SELECT id FROM person);
DELETE FROM artwork WHERE subject_type = 'person' AND subject_id NOT IN (SELECT id FROM person);
DELETE FROM artwork_miss WHERE subject_type = 'person' AND subject_id NOT IN (SELECT id FROM person);
