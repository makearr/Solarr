-- Size limits per quality are opt-in: a guide rail, not a rule. Takes back the ones 0065 put on the shipped video profiles.
UPDATE quality_profile SET rules_json = json_remove(rules_json, '$.require.size', '$.targets.overall_bitrate') WHERE id IN (4, 5, 6, 7, 8, 9, 11, 12, 13, 14, 20, 21);
