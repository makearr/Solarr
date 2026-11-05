-- Whether this account's requests are approved at once; without it a request waits for an administrator (Seerr's model).
ALTER TABLE app_user ADD COLUMN auto_approve INTEGER NOT NULL DEFAULT 0 CHECK (auto_approve IN (0, 1));
