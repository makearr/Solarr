-- One schedule per metadata pass for every media type: each new task keeps the overrides of the music task it grew
-- from; the TV tasks folded into them had no others, so theirs go.
UPDATE OR IGNORE instance_setting SET key = 'schedule.refresh_works.' || substr(key, length('schedule.enrichment.') + 1)
WHERE key LIKE 'schedule.enrichment.%';
UPDATE OR IGNORE instance_setting SET key = 'schedule.refresh_people.' || substr(key, length('schedule.refresh_artists.') + 1)
WHERE key LIKE 'schedule.refresh_artists.%';
UPDATE OR IGNORE instance_setting SET key = 'schedule.pictures.' || substr(key, length('schedule.artwork.') + 1)
WHERE key LIKE 'schedule.artwork.%';
DELETE FROM instance_setting
WHERE key LIKE 'schedule.enrichment.%' OR key LIKE 'schedule.refresh_artists.%' OR key LIKE 'schedule.artwork.%'
   OR key LIKE 'schedule.refresh_series.%' OR key LIKE 'schedule.tv_artwork.%';
