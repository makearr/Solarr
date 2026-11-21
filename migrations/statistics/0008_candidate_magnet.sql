-- A magnet link some indexers list beside the .torrent link, for when that link is dead; empty when the indexer gave none.
ALTER TABLE candidate ADD COLUMN magnet_url TEXT NOT NULL DEFAULT '';
