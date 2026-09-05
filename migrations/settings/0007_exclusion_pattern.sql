-- ADR-53 part 1: unwanted files by shape, never by content. Policy, so
-- settings.db (the hash-based blocked_content half lives in library.db,
-- since it is a fact about content, not a policy -- see its own migration).
-- Schema per architecture.md section 5.8.
CREATE TABLE exclusion_pattern (
  id           INTEGER PRIMARY KEY,
  pattern      TEXT    NOT NULL,          -- glob against the file name, or *.ext
  action       TEXT    NOT NULL,          -- ignore | refuse
  media_type   TEXT,                      -- NULL = all; .iso is poison in music, fine in roms
  category_id  INTEGER,                   -- NULL = every category of that media type
  note         TEXT    NOT NULL DEFAULT '',  -- why this default exists; shown in the UI
  builtin      INTEGER NOT NULL DEFAULT 0,   -- shipped default; editable and removable
  enabled      INTEGER NOT NULL DEFAULT 1,
  UNIQUE(pattern, media_type, category_id)
);

-- Shipped defaults (architecture.md ADR-53 section 1): conservative, fully
-- editable, each with a note saying why it exists. `ignore` skips silently;
-- `refuse` poisons the whole unit it is found in -- the half nobody ships.
INSERT INTO exclusion_pattern (pattern, action, media_type, note, builtin) VALUES
  ('*.sfv',      'ignore', NULL, 'Checksum sidecar, not media', 1),
  ('*.srr',      'ignore', NULL, 'ReScene recovery data, not media', 1),
  ('*.par2',     'ignore', NULL, 'Parity recovery data, not media', 1),
  ('*.nfo',      'ignore', NULL, 'Release info text, not media', 1),
  ('*.txt',      'ignore', NULL, 'Plain text, not media', 1),
  ('*.url',      'ignore', NULL, 'Shortcut file, not media', 1),
  ('Thumbs.db',  'ignore', NULL, 'Windows thumbnail cache', 1),
  ('.DS_Store',  'ignore', NULL, 'macOS folder metadata', 1),
  ('sample.*',   'ignore', NULL, 'A promotional sample, not the release itself', 1),
  ('*.exe',      'refuse', NULL, 'An executable beside media is the classic shape of a fake', 1),
  ('*.scr',      'refuse', NULL, 'A screensaver executable is still an executable', 1),
  ('*.bat',      'refuse', NULL, 'A batch script beside media is a red flag, not clutter', 1),
  ('*.cmd',      'refuse', NULL, 'A command script beside media is a red flag, not clutter', 1),
  ('*.com',      'refuse', NULL, 'A legacy DOS executable is still an executable', 1),
  ('*.msi',      'refuse', NULL, 'An installer beside media is a red flag, not clutter', 1),
  ('*.vbs',      'refuse', NULL, 'A VBScript beside media is a red flag, not clutter', 1),
  ('*.lnk',      'refuse', NULL, 'A Windows shortcut can point at an executable payload', 1),
  ('*.iso',      'refuse', 'music', 'A disc image is poison in a music release; legitimate in ROMs', 1);
