import { describe, expect, it } from "vitest";

import { editionSummary, factsOf, outcomeLabel, verdictOf } from "./identificationEvidence";

describe("factsOf", () => {
  it("keys facts by kind, keeping the first of a repeated kind", () => {
    expect(
      factsOf([
        { kind: "title", detail: "Kebilegev Bibihi" },
        { kind: "title", detail: "later" },
        { kind: "distance", detail: "0.042" },
      ]),
    ).toEqual({ title: "Kebilegev Bibihi", distance: "0.042" });
  });

  it("reads missing evidence as no facts", () => {
    expect(factsOf(undefined)).toEqual({});
  });
});

describe("verdictOf", () => {
  it("puts a contradiction ahead of acceptance", () => {
    expect(verdictOf({ accepted: "false", contradicted: "true" })).toBe("Track lengths or recordings differ");
    expect(verdictOf({ accepted: "true", contradicted: "false" })).toBe("Fits");
    expect(verdictOf({ accepted: "false", contradicted: "false" })).toBe("Does not fit well enough");
    expect(verdictOf({})).toBe("");
  });
});

describe("editionSummary", () => {
  it("joins what is known and skips what is not", () => {
    expect(
      editionSummary({ edition_title: "Kebilegev Bibihi", edition_date: "2017-10-13", edition_country: "", edition_tracks: "13" }),
    ).toBe("Kebilegev Bibihi · 2017-10-13 · 13 tracks");
  });
});

describe("outcomeLabel", () => {
  it("explains an album outcome and passes an unknown one through", () => {
    expect(outcomeLabel("ambiguous")).toBe("Several albums fit about equally well -- your choice");
    expect(outcomeLabel("something_new")).toBe("something_new");
  });
});
