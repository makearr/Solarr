-- The music cutoff check judged TV season items as albums and marked thousands UPGRADABLE; a season is complete or
-- not, and what is below a video library's target is worked out live (Wanted, Cutoff unmet).
UPDATE desired_item SET state = 'COMPLETE'
WHERE state = 'UPGRADABLE' AND work_id IN (SELECT id FROM work WHERE media_type = 'tv');
