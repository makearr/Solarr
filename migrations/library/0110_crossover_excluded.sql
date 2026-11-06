-- A part a person took out of an event Solarr found, kept by the event's name so detection does not put it back.
CREATE TABLE crossover_excluded (
  name_key TEXT NOT NULL,
  part_key TEXT NOT NULL,
  PRIMARY KEY (name_key, part_key)
) WITHOUT ROWID;
