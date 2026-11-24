import { expect, test } from "vitest";

import { reasonText } from "./transcodeReason";

test("a reason is its sentence with what was wanted and what the file has", () => {
  expect(reasonText({ code: "audio_codec", stream: 0, want: "aac", have: "flac" })).toBe("the audio codec differs (wanted aac, has flac)");
  expect(reasonText({ code: "nothing_to_do", stream: -1 })).toBe("it already matches the target");
  expect(reasonText({ code: "some_new_code", stream: -1 })).toBe("some new code");
});
