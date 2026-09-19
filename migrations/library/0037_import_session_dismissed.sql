-- A finished session stays on the Import screen for ever, so a week of
-- watched-folder runs buries the one being worked on (2026-09-19 review:
-- "the items finished should able to be dismissed").
--
-- Dismissal hides the row, it never deletes it: a dismissed session is still
-- the record of what it placed, and revert still reaches it by id.
ALTER TABLE import_session ADD COLUMN dismissed_at INTEGER;
