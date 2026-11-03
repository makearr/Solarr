-- A foreign key's child column needs an index, or every delete of its parent scans the child table: a person 8.7 ms
-- (55k works), an edition 1.1 ms (59k desired items), measured on a live library.
CREATE INDEX work_by_lead_person ON work(lead_person_id) WHERE lead_person_id IS NOT NULL;
CREATE INDEX desired_by_edition ON desired_item(edition_id) WHERE edition_id IS NOT NULL;

-- Covered by arr_link's own primary key, which starts with instance_id.
DROP INDEX arr_link_by_instance;
