-- RSS sync (M1c) finds wanted albums by the artist a release names, ignoring case.
CREATE INDEX person_name_nocase ON person(name COLLATE NOCASE);
