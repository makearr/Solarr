-- Media-type vocabulary for a work, and image attributes for galleries.

-- Classification a media type gives its works: album type for music, one-shot or
-- annual for comics, special for TV. Generic on purpose (ADR-23): core stores the
-- pairs and a module owns the vocabulary, so a new type needs no core column.
--
-- Lidarr users monitor on exactly this ("studio albums, not live"), which needs a
-- value to filter on rather than a behaviour.
CREATE TABLE work_classification (
  work_id INTEGER NOT NULL REFERENCES work(id) ON DELETE CASCADE,
  -- 'primary' admits one value per work; 'secondary' admits many.
  kind    TEXT    NOT NULL,
  value   TEXT    NOT NULL,
  source  TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (work_id, kind, value)
) WITHOUT ROWID;
CREATE INDEX work_classification_lookup ON work_classification(kind, value);

-- Image attributes, for picture galleries. Null on audio and video, as the
-- audio and video columns are null here: one interned table keeps quality
-- profiles and scoring on a single path.
ALTER TABLE media_attributes ADD COLUMN image_format TEXT;
ALTER TABLE media_attributes ADD COLUMN colour_depth INTEGER;
ALTER TABLE media_attributes ADD COLUMN page_count INTEGER;
