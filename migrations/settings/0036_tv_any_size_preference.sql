-- The shipped TV profile Any ranked a 150 MB 480p release and a 5 GB 2160p one by resolution alone; two small additive
-- penalties now keep a starved or bloated file behind its peers. Only an untouched shipped profile is changed.
UPDATE quality_profile
SET rules_json = '{"quality_profile":"Any","compare":["resolution","source","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"],["dvd"]]},"scoring":[{"name":"Starved (under 0.4 Mbps)","score":-15,"specs":[{"field":"video_bitrate","lt":400000,"required":true}]},{"name":"Bloated (over 25 Mbps)","score":-15,"specs":[{"field":"video_bitrate","gt":25000000,"required":true}]}]}'
WHERE name = 'Any' AND media_type = 'tv'
  AND rules_json = '{"quality_profile":"Any","compare":["resolution","source","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"],["dvd"]]}}';
