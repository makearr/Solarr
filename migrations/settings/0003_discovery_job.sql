-- Autonomous discovery jobs (ADR-38). Policy, so it stays in settings.db beside
-- category rather than in library.db with the graph it reads from -- a person, tag
-- or organisation id a subscription-sourced job names lives in library.db and has no
-- FK here (ADR-25, section 4.3); it is carried inside source_json instead.
-- Pure ASCII, or sqlc emits wrong SQL that still compiles.
CREATE TABLE discovery_job (
  id            INTEGER PRIMARY KEY,
  name          TEXT    NOT NULL UNIQUE,
  -- {kind: library|subscription|list, provider, list}
  source_json   TEXT    NOT NULL,
  media_type    TEXT,
  category_id   INTEGER REFERENCES category(id),
  -- ratings, votes, year, genre, language, tags, people
  filters_json  TEXT    NOT NULL,
  -- weights across rating sources, e.g. {imdb: 0.4, tmdb: 0.3, trakt: 0.3}
  scoring_json  TEXT,
  -- Top-N per run, never all-or-nothing -- a misconfigured job cannot add
  -- everything a list holds in one pass.
  limit_per_run INTEGER NOT NULL DEFAULT 10,
  action        TEXT    NOT NULL CHECK (action IN ('request', 'auto_add', 'notify')),
  schedule      TEXT,
  -- Off until previewed: run the job, show what it would add and why, change
  -- nothing (ADR-38's "preview before enabling").
  enabled       INTEGER NOT NULL DEFAULT 0 CHECK (enabled IN (0, 1)),
  last_run_at   INTEGER
);
CREATE INDEX discovery_job_enabled ON discovery_job(enabled) WHERE enabled = 1;
