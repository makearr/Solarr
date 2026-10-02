-- Delay profile (D16): a library may prefer one protocol and hold the other's releases back for a while.
ALTER TABLE category ADD COLUMN preferred_protocol TEXT NOT NULL DEFAULT '' CHECK (preferred_protocol IN ('', 'usenet', 'torrent'));
ALTER TABLE category ADD COLUMN protocol_delay_minutes INTEGER NOT NULL DEFAULT 0 CHECK (protocol_delay_minutes >= 0);
