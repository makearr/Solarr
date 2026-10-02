-- Shipped TV quality profiles (stage 8 T7), the same ADR-40 rule shape as music's: hard gates, then tiers in the order
-- resolution, source, codec, HDR, audio, then the additive score. Data only; fully editable and removable.
INSERT INTO quality_profile (name, rules_json, upgrade_json, media_type) VALUES
  (
    'WEB 1080p',
    '{"quality_profile":"WEB 1080p","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","audio_channels","audio_codec","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"]],"source":[["webdl"],["webrip"],["bluray","remux"],["hdtv"]],"video_codec":[["h264"],["h265","av1"]],"audio_channels":[["5.1"],["2.0"]],"audio_codec":[["eac3","ac3"],["aac"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'tv'
  ),
  (
    'HD 720p/1080p',
    '{"quality_profile":"HD 720p/1080p","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","audio_channels","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"]],"source":[["bluray","remux"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]],"audio_channels":[["5.1"],["2.0"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'tv'
  ),
  (
    'UHD 2160p',
    '{"quality_profile":"UHD 2160p","require":{"resolution":{"min":"1080p","max":"2160p"},"reject_codecs":["xvid","divx"]},"compare":["resolution","source","video_codec","hdr","audio_channels","audio_codec","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]],"hdr":[["dolby-vision"],["hdr10plus","hdr10"],["hdr","hlg"]],"audio_channels":[["7.1"],["5.1"],["2.0"]],"audio_codec":[["truehd","dtshd"],["eac3","dts"],["ac3","aac"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'tv'
  ),
  (
    'Space conscious (x265 1080p)',
    '{"quality_profile":"Space conscious (x265 1080p)","require":{"resolution":{"min":"720p","max":"1080p"},"reject_codecs":["xvid","divx"]},"compare":["video_codec","resolution","bitrate_distance","source","score"],"tiers":{"video_codec":[["h265","av1"],["h264"]],"resolution":[["1080p","1080i"],["720p"]],"source":[["webdl","bluray"],["webrip"],["hdtv"]]},"targets":{"bitrate":[{"when":{"video_codec":"h265","resolution":"1080p"},"min":"1Mbps","ideal":"3Mbps","max":"6Mbps"},{"when":{"video_codec":"h264","resolution":"1080p"},"min":"2Mbps","ideal":"5Mbps","max":"9Mbps"}]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'tv'
  ),
  (
    'Anime 1080p',
    '{"quality_profile":"Anime 1080p","require":{"resolution":{"min":"720p","max":"1080p"},"audio_language":{"any_of":["ja","en"]}},"compare":["resolution","source","video_codec","score"],"tiers":{"resolution":[["1080p","1080i"],["720p"]],"source":[["bluray","remux"],["webdl"],["webrip"],["hdtv"]],"video_codec":[["h265","av1"],["h264"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'tv'
  ),
  (
    'SD',
    '{"quality_profile":"SD","require":{"resolution":{"min":"480p","max":"576p"}},"compare":["resolution","source","score"],"tiers":{"resolution":[["576p","480p"]],"source":[["dvd"],["webdl","webrip"],["hdtv"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'tv'
  ),
  (
    'Any',
    '{"quality_profile":"Any","compare":["resolution","source","score"],"tiers":{"resolution":[["2160p"],["1080p","1080i"],["720p"],["576p","480p"]],"source":[["remux"],["bluray"],["webdl"],["webrip"],["hdtv"],["dvd"]]}}',
    '{"min_delta":15,"min_file_age":"24h","max_per_window":3,"window":"168h"}',
    'tv'
  );
