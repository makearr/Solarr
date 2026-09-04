-- The claimed half of ADR-39 has nowhere to put a bitrate claim today:
-- media_attributes has no bitrate column at all, and the existing scalar
-- bitrate columns on media_object are documented measured-only ("a release
-- name states none of them") -- true for video, plainly false for music,
-- where "-320-" and "V0" are the commonest claims a release name carries
-- (internal/parse's own BitrateKbps field, todo 4.1/4.2). These stay on
-- media_attributes rather than media_object because they are categorical
-- (a handful of distinct values across the whole library), unlike the
-- per-file scalar bitrates media_object already carries for the measured
-- side -- interning still holds (section 5.2's own reasoning for the table).
ALTER TABLE media_attributes ADD COLUMN bitrate_kbps INTEGER;
-- "cbr" or "vbr" -- empty when unstated. A negative bitrate_kbps (this
-- project's own convention for "a VBR preset was named, not a fixed rate",
-- internal/parse's vbrPresetRe) already implies vbr, but a bare "192kbps"
-- claim needs its own field to say it is fixed rather than assumed.
ALTER TABLE media_attributes ADD COLUMN bitrate_mode TEXT;
-- The literal preset token ("V0", "V2") when bitrate_mode = 'vbr' and the
-- release named one -- distinct from bitrate_kbps, which stores the
-- negative sentinel, not the preset's own name.
ALTER TABLE media_attributes ADD COLUMN vbr_preset TEXT;
