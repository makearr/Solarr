-- Two syncs of one instance must not run together: each records what it created for its own revert.
UPDATE arr_import SET state = 'FAILED', error = 'superseded'
WHERE state = 'RUNNING' AND id NOT IN (SELECT MAX(id) FROM arr_import WHERE state = 'RUNNING' GROUP BY instance_id);
CREATE UNIQUE INDEX arr_import_one_running ON arr_import(instance_id) WHERE state = 'RUNNING';
