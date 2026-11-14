-- Every foreign key column that leads no index: a parent delete scanned the whole child table for each deleted row.
-- Nullable columns get partial indexes, which stay tiny while most rows are NULL.

CREATE INDEX IF NOT EXISTS media_object_by_claimed_attributes ON media_object(claimed_attributes_id) WHERE claimed_attributes_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS request_by_work ON request(work_id) WHERE work_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS blocklisted_release_by_desired_item ON blocklisted_release(desired_item_id) WHERE desired_item_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS import_decision_by_incumbent_object ON import_decision(incumbent_object_id) WHERE incumbent_object_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS import_decision_by_acquisition ON import_decision(acquisition_id) WHERE acquisition_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS import_decision_by_desired_item ON import_decision(desired_item_id);
CREATE INDEX IF NOT EXISTS recycle_bin_by_acquisition ON recycle_bin(acquisition_id) WHERE acquisition_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS recycle_bin_by_media_object ON recycle_bin(media_object_id) WHERE media_object_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS acquisition_file_by_library_path ON acquisition_file(library_path_id) WHERE library_path_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS acquisition_file_by_acquisition ON acquisition_file(acquisition_id);
CREATE INDEX IF NOT EXISTS collection_external_id_by_collection ON collection_external_id(collection_id);
CREATE INDEX IF NOT EXISTS group_observation_by_media_object ON group_observation(media_object_id) WHERE media_object_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS import_unit_by_edition ON import_unit(edition_id) WHERE edition_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS import_unit_by_work ON import_unit(work_id) WHERE work_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS import_unit_file_by_library_path ON import_unit_file(library_path_id) WHERE library_path_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS pack_item_by_import_unit ON pack_item(import_unit_id) WHERE import_unit_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS pack_item_by_edition ON pack_item(edition_id) WHERE edition_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS arr_import_item_by_desired_item ON arr_import_item(desired_item_id) WHERE desired_item_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS arr_import_item_by_work ON arr_import_item(work_id) WHERE work_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS arr_import_item_by_subscription ON arr_import_item(subscription_id) WHERE subscription_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS arr_import_item_by_person ON arr_import_item(person_id) WHERE person_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS manual_identification_by_work ON manual_identification(work_id);
CREATE INDEX IF NOT EXISTS ready_queue_by_desired_item ON ready_queue(desired_item_id);

-- Download history of one item, newest first; also the lookup behind the foreign key from acquisition to desired_item.
CREATE INDEX IF NOT EXISTS acquisition_by_item ON acquisition(desired_item_id, created_at DESC) WHERE desired_item_id IS NOT NULL;
