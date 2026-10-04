import { expect, test } from "vitest";

import { fromLegacy, resolve } from "./theme";
import { flipped } from "./useTheme";

test("a scheme follows the mode, and the OS under system", () => {
  expect(resolve("light", "lidarr", true).id).toBe("lidarr");
  expect(resolve("dark", "lidarr", false).id).toBe("lidarr-dark");
  expect(resolve("system", "lidarr", true).id).toBe("lidarr-dark");
  expect(resolve("system", "solarr", false).id).toBe("light");
});

test("a scheme with one variant keeps it whatever the mode", () => {
  expect(resolve("light", "nord", false).id).toBe("nord");
  expect(resolve("dark", "paper", true).id).toBe("paper");
});

test("a theme stored before the split becomes its scheme and base", () => {
  expect(fromLegacy("sonarr-dark")).toEqual({ mode: "dark", scheme: "sonarr" });
  expect(fromLegacy("light")).toEqual({ mode: "light", scheme: "solarr" });
  expect(fromLegacy(null)).toEqual({ mode: "system", scheme: "solarr" });
});

test("the light/dark switch moves to the opposite of what is showing, whatever the mode says", () => {
  expect([
    flipped("light", "solarr", true),
    flipped("dark", "solarr", false),
    flipped("system", "solarr", true),
    flipped("system", "solarr", false),
  ]).toEqual(["dark", "light", "light", "dark"]);
});
