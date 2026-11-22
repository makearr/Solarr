import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import { tr } from "./i18nRich";

describe("tr", () => {
  it("puts each element where its placeholder stands and leaves the text around it", () => {
    expect(renderToStaticMarkup(<p>{tr("replace.move", { name: <code>x</code> })}</p>)).toBe("<p>Move what uses <code>x</code> to</p>");
  });

  it("keeps a placeholder no element was given for", () => {
    expect(renderToStaticMarkup(<p>{tr("replace.move", {})}</p>)).toBe("<p>Move what uses {name} to</p>");
  });
});
