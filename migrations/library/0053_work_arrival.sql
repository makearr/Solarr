-- When each work's newest file arrived: the dashboard's Recently added orders by it, not by when
-- the album was added as wanted (found 2026-09-30: an imported album never showed there).
CREATE TABLE work_arrival (
  work_id    INTEGER PRIMARY KEY REFERENCES work(id) ON DELETE CASCADE,
  arrived_at INTEGER NOT NULL
);
CREATE INDEX work_arrival_at ON work_arrival(arrived_at, work_id);

INSERT INTO work_arrival (work_id, arrived_at)
SELECT work_id, MAX(first_seen_at) FROM work_file GROUP BY work_id;

CREATE TRIGGER work_file_arrival AFTER INSERT ON work_file
BEGIN
  INSERT INTO work_arrival (work_id, arrived_at) VALUES (NEW.work_id, NEW.first_seen_at)
  ON CONFLICT (work_id) DO UPDATE SET arrived_at = MAX(arrived_at, excluded.arrived_at);
END;
