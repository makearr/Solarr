-- Retiring an expired candidate asks whether a decision still refers to it; without this index that is a scan
-- of every decision per candidate, and so is the foreign key's own cascade.
CREATE INDEX decision_by_candidate ON candidate_decision(candidate_id);
