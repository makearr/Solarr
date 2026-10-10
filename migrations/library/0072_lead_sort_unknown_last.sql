-- A work with no lead person sorts after every name, not before: an empty or missing sort name orders as U+10FFFF,
-- the highest code point. The index follows the expression so the order stays indexed.
DROP INDEX work_lead_person_sort;
CREATE INDEX work_lead_person_sort
  ON work(COALESCE(NULLIF(lead_person_sort_name, ''), char(1114111)) COLLATE NOCASE, sort_title, id);
