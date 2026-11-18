-- A source folder's earlier imports are looked up by path on every import; without an index that read every session.
CREATE INDEX idx_import_session_source ON import_session(source_path, started_at DESC);
