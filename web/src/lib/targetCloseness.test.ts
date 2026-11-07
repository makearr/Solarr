import { describe, expect, it } from "vitest";

import { sizeOff, sizeText, sizeTone, targetClosenessChip } from "./targetCloseness";

describe("sizeOff", () => {
  it("is the same for a factor and its inverse", () => {
    expect(sizeOff(50)).toBe(0.5);
    expect(sizeOff(200)).toBe(0.5);
    expect(sizeOff(100)).toBe(0);
  });
});

describe("sizeTone", () => {
  it("grades the distance", () => {
    expect([100, 94, 108, 85, 120, 60, 160, 40, 618, 50, 200].map(sizeTone)).toEqual([
      "ok", "ok", "ok", "caution", "caution", "orange", "orange", "danger", "danger", "danger", "danger",
    ]);
  });
});

describe("sizeText", () => {
  it("says which side of the target the file is on", () => {
    expect(sizeText(94)).toBe("94% of the target");
    expect(sizeText(53)).toBe("53%, below the target");
    expect(sizeText(618)).toBe("618% above the target");
  });
});

describe("targetClosenessChip", () => {
  it("is nothing without a size", () => {
    expect(targetClosenessChip({} as never)).toBeNull();
  });
  it("lists the expected size and the gaps on hover", () => {
    const chip = targetClosenessChip({ target_size_pct: 82, target_expected_kbps: 1652, target_gaps: [{ dimension: "CRF", want: "28", have: "21" }] } as never);
    expect(chip).toEqual({
      text: "82%, below the target",
      title: "A file on the target is about 12.4 MB/min (743 MB/h, 1652 kbit/s)\nDiffers from the target in\nCRF: 21 (wanted 28)",
      tone: "caution",
    });
  });
});
