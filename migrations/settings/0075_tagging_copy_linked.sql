-- A tagged file that a download still links to is skipped unless the library chooses to write a separate copy.
ALTER TABLE category_file_management ADD COLUMN tag_copy_linked_files INTEGER NOT NULL DEFAULT 0 CHECK (tag_copy_linked_files IN (0, 1));
