-- Listing events of a few kinds newest first walked the whole log for a rare kind; these indexes lead with the filter and end in the order.
CREATE INDEX event_kind_id ON event_log(kind, id DESC);
CREATE INDEX event_subject_id ON event_log(subject_type, subject_id, id DESC);
