import { expect, test } from "vitest";

import { headline, rows } from "./ReplacementHistory";

const tier = (dimension: string, label: string) => ({ dimension, rank: 0, label, detail: "" });

test("a replacement says what decided it, and a tie keeps the owned file", () => {
  expect(headline({ outcome: "IMPORT", decided_by: "resolution" })).toBe("The download replaced the owned file: decided by resolution.");
  expect(headline({ outcome: "REJECT_WORSE", decided_by: "score" })).toBe(
    "The owned file was kept: the tiers tied and the score decided; a tie keeps what is owned.",
  );
});

test("the two files line up by dimension, a missing one shown as a dash", () => {
  expect(
    rows({ media_object_id: 1, score: 0, tiers: [tier("resolution", "1080p"), tier("codec", "hevc")] }, { media_object_id: 2, score: 0, tiers: [tier("resolution", "720p")] }),
  ).toEqual([
    { dimension: "resolution", arrived: "1080p", incumbent: "720p" },
    { dimension: "codec", arrived: "hevc", incumbent: "–" },
  ]);
});
