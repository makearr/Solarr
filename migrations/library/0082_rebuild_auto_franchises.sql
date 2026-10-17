-- The rules that group series changed (apostrophes, connectors, a genre or network in common, provider franchises taking in
-- the rest of a family): the groups Solarr made itself are made again, and every series is asked about once more.
DELETE FROM collection WHERE source = 'auto';
DELETE FROM work_franchise_check;
