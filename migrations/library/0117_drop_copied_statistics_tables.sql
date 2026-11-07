-- Releases, decisions and the rollups moved to statistics.db (revision 2, D7) and were copied there on every start since; the originals go.
DROP TABLE IF EXISTS candidate_decision;
DROP TABLE IF EXISTS candidate;
DROP TABLE IF EXISTS decision_rollup;
DROP TABLE IF EXISTS stat_rollup;
