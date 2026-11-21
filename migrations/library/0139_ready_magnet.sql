-- The magnet link a held release was listed with, for when its .torrent link is dead; empty when there was none.
ALTER TABLE ready_queue ADD COLUMN magnet_url TEXT NOT NULL DEFAULT '';
