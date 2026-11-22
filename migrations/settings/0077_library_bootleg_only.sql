-- A music library lists release groups that only exist as bootlegs, promos or pseudo-releases only when it is in this table.
CREATE TABLE category_bootleg_only (
  category_id INTEGER PRIMARY KEY REFERENCES category(id) ON DELETE CASCADE
);
