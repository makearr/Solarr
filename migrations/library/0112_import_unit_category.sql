-- The library a Best match import routed this unit to; NULL places it into its session's. No FK: category lives in settings.db.
ALTER TABLE import_unit ADD COLUMN category_id INTEGER;
