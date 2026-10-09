-- When RSS last showed a release for each wanted item, newest first-seen: what "fresh" (ADR-55, first seen on an
-- indexer within 48 h) reads, so the planner and the Fresh filter need no candidate or decision once those move to
-- statistics.db (revision 2, D7).
CREATE TABLE rss_sighting (
  desired_item_id INTEGER PRIMARY KEY REFERENCES desired_item(id) ON DELETE CASCADE,
  seen_at         INTEGER NOT NULL
);
CREATE INDEX rss_sighting_seen ON rss_sighting(seen_at);
INSERT INTO rss_sighting (desired_item_id, seen_at)
SELECT rd.desired_item_id, MAX(rc.first_seen_at)
FROM candidate_decision rd JOIN candidate rc ON rc.id = rd.candidate_id
WHERE rc.source = 'rss'
GROUP BY rd.desired_item_id;
