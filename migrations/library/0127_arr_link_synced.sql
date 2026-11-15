-- The monitored value last pushed to or pulled from the *Arr for a link; null until one has been, which keeps "Solarr wins".
ALTER TABLE arr_link ADD COLUMN synced_monitored INTEGER;
