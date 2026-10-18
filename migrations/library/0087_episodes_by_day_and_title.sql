-- The same-broadcast suggestions pair episodes of different series by day and title; without this index that join read
-- every episode once per episode, minutes for a library of a thousand series.
CREATE INDEX work_part_date_title ON work_part(date, LOWER(title));
