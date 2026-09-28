-- A reaped lease (a crash, a suspended host) is not a failed attempt; it has its own budget.
ALTER TABLE job ADD COLUMN reclaims INTEGER NOT NULL DEFAULT 0;
