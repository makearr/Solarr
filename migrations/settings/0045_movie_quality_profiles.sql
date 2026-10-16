-- Shipped movie quality profiles (stage 10), the same ADR-40 rule shape as TV's: hard gates, tiers, then the additive
-- score. A film is judged by its source before its resolution being re-encoded, so a bluray tier leads the web ones.
-- Names carry a Movie prefix because a profile name is unique across media types. Data only; fully editable and removable.
INSERT INTO quality_profile (name, rules_json, upgrade_json, media_type) VALUES
  (
    'Movie HD 1080p',
    '{"quality_profile":"Movie HD 1080p","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","audio_channels","audio_codec","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["bluray","remux"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h264"],["h265","av1"]],"audio_channels":[["5.1"],["2.0"]],"audio_codec":[["eac3","ac3","dts"],["aac"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'movie'
  ),
  (
    'Movie UHD 2160p',
    '{"quality_profile":"Movie UHD 2160p","require":{"resolution":{"min":"1080p","max":"2160p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","hdr","audio_channels","audio_codec","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p","360p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]],"hdr":[["dolby-vision"],["hdr10plus","hdr10"],["hdr","hlg"]],"audio_channels":[["7.1"],["5.1"],["2.0"]],"audio_codec":[["truehd","dtshd"],["eac3","dts"],["ac3","aac"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'movie'
  ),
  (
    'Movie space conscious (x265 1080p)',
    '{"quality_profile":"Movie space conscious (x265 1080p)","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["video_codec","resolution","bitrate_distance","source","score"],"tiers":{"video_codec":[["h265","av1"],["h264"]],"resolution":[["1080p","1080i"],["720p"],["576p","480p","360p"],["2160p"]],"source":[["webdl","bluray"],["webrip"],["hdtv"]]},"targets":{"bitrate":[{"when":{"video_codec":"h265","resolution":"1080p"},"min":"1.5Mbps","ideal":"4Mbps","max":"8Mbps"},{"when":{"video_codec":"h264","resolution":"1080p"},"min":"3Mbps","ideal":"7Mbps","max":"12Mbps"}]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'movie'
  ),
  (
    'Movie SD',
    '{"quality_profile":"Movie SD","require":{"resolution":{"min":"480p","max":"576p"}},"compare":["resolution","source","score"],"tiers":{"resolution":[["576p","480p"],["360p"],["720p"],["1080p","1080i"],["2160p"]],"source":[["dvd"],["webdl","webrip"],["hdtv"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'movie'
  ),
  (
    'Movie Any',
    '{"quality_profile":"Movie Any","compare":["resolution","source","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"],["dvd"]]},"scoring":[{"name":"Starved (under 1 Mbps)","score":-15,"specs":[{"field":"video_bitrate","lt":1000000,"required":true}]},{"name":"Bloated (over 60 Mbps)","score":-15,"specs":[{"field":"video_bitrate","gt":60000000,"required":true}]}]}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'movie'
  );
