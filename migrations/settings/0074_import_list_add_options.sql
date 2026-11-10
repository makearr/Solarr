ALTER TABLE import_list ADD COLUMN root_folder_id INTEGER;
ALTER TABLE import_list ADD COLUMN search_on_add INTEGER NOT NULL DEFAULT 0 CHECK (search_on_add IN (0, 1));
