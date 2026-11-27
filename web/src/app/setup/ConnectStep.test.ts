import { expect, test } from "vitest";

import { uniqueName } from "./ConnectStep";

test("a default name is the app's own, numbered once it is taken", () => {
  expect(uniqueName("Radarr", [])).toBe("Radarr");
  expect(uniqueName("Radarr", ["radarr"])).toBe("Radarr 2");
  expect(uniqueName("Radarr", ["Radarr", "Radarr 2"])).toBe("Radarr 3");
  expect(uniqueName("Sonarr", ["Radarr"])).toBe("Sonarr");
});
