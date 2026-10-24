-- The rules a library suggests itself by for a new work, as JSON; NULL follows its kind's shipped rules
-- (plan 10-libraries L5). A library's further media types use its rules.
ALTER TABLE category ADD COLUMN rules_json TEXT;
