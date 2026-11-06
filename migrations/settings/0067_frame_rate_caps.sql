-- "30 fps" in the shipped presets means at most 30: transcodes keep a 24 or 25 fps source's rate (most of a real library is 24 fps).
UPDATE quality_target SET name = replace(name, ', 30 fps,', ', source fps (max 30),'), max_frame_rate = 30, frame_rate = NULL WHERE builtin = 1 AND frame_rate = 30;
UPDATE target_profile SET recommended_target = replace(recommended_target, ', 30 fps,', ', source fps (max 30),') WHERE builtin = 1;
