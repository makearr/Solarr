/// <reference types="vite/client" />
import ts from "typescript";
import { describe, expect, it } from "vitest";

import { hardcodedStringAllowance } from "./i18nAllowance";

const sources = import.meta.glob<string>(["../app/**/*.tsx", "../ui/**/*.tsx", "!**/*.test.tsx"], { query: "?raw", import: "default", eager: true });

const sentence = /^[A-Z][a-z]+(?: [A-Za-z0-9,'.-]+){2,}$/;
const label = /^[A-Z][a-z]+(?: [A-Za-z0-9,'.-]+){1,}$/;
const labelled = new Set(["title", "placeholder", "aria-label"]);

// Counts the English sentences a screen writes into JSX instead of taking them from lib/i18n.
function hardcodedStrings(path: string, text: string): number {
  const file = ts.createSourceFile(path, text, ts.ScriptTarget.ES2022, true, ts.ScriptKind.TSX);
  let found = 0;
  const visit = (node: ts.Node) => {
    if (ts.isJsxText(node) && sentence.test(node.text.trim().replace(/\s+/g, " "))) found++;
    if (ts.isJsxAttribute(node) && labelled.has(node.name.getText(file)) && node.initializer && ts.isStringLiteral(node.initializer) && label.test(node.initializer.text)) found++;
    ts.forEachChild(node, visit);
  };
  visit(file);
  return found;
}

describe("i18n coverage", () => {
  it("lets no screen grow its count of English literals; lower a file's allowance when its strings move into lib/i18n", () => {
    const actual: Record<string, number> = {};
    for (const [path, text] of Object.entries(sources)) {
      const count = hardcodedStrings(path, text);
      if (count > 0) actual[path.replace("../", "")] = count;
    }
    const grew = Object.fromEntries(Object.entries(actual).filter(([path, count]) => count > (hardcodedStringAllowance[path] ?? 0)));
    expect(grew).toEqual({});
  });
});
