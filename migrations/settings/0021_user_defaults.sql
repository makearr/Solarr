-- Per-user limits set by an administrator (2026-09-24): the interface level an account starts at
-- and the most it may raise itself to, and what it is granted on each library created later.
ALTER TABLE app_user ADD COLUMN ui_tier_default TEXT NOT NULL DEFAULT 'normal'
  CHECK (ui_tier_default IN ('normal', 'advanced', 'expert'));
ALTER TABLE app_user ADD COLUMN ui_tier_max TEXT NOT NULL DEFAULT 'expert'
  CHECK (ui_tier_max IN ('normal', 'advanced', 'expert'));
ALTER TABLE app_user ADD COLUMN new_library_permission TEXT NOT NULL DEFAULT 'none'
  CHECK (new_library_permission IN ('none', 'read', 'request', 'manage'));
UPDATE app_user SET new_library_permission = 'manage' WHERE role = 'admin';
