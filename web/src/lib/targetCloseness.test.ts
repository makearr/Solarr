import { describe, expect, it } from "vitest";

import { targetClosenessChip } from "./targetCloseness";

describe("targetClosenessChip", () => {
  it("says nothing when the library has no target", () => {
    expect(targetClosenessChip({} as never)).toBeNull();
  });

  it("lists each gap and warns below 100", () => {
    const chip = targetClosenessChip({ target_closeness_pct: 82, target_gaps: [{ dimension: "CRF", want: "28", have: "21" }] } as never);
    expect(chip).toEqual({ text: "82% of the target", title: "Differs from the target in\nCRF: 21 (wanted 28)", tone: "warn" });
  });

  it("is ok at 100", () => {
    expect(targetClosenessChip({ target_closeness_pct: 100, target_gaps: [] } as never)?.tone).toBe("ok");
  });
});
