import { describe, expect, it } from "vitest";

import type { components } from "@/api/schema";
import { fieldOptions, fieldValues, kindSecret, missingFields, newLabel } from "@/lib/downloaderKinds";

type DownloaderKind = components["schemas"]["DownloaderKind"];

const blackhole: DownloaderKind = {
  impl: "torrentblackhole", label: "Torrent Blackhole", protocol: "torrent", has_address: false, user_name: false,
  keeps_completed: true, categories: false,
  fields: [
    { key: "drop_folder", kind: "path", label: "Torrent folder", required: true },
    { key: "watch_folder", kind: "path", label: "Watch folder", required: true },
    { key: "save_magnet_files", kind: "bool", label: "Save magnet links", required: false },
  ],
};

const transmission: DownloaderKind = {
  impl: "transmission", label: "Transmission", protocol: "torrent", has_address: true, address_placeholder: "http://t:9091",
  user_name: true, secret_label: "Password", keeps_completed: true, categories: true,
  fields: [{ key: "url_base", kind: "text", label: "URL base", required: false }],
};

describe("fieldOptions", () => {
  it("keeps only the kind's own settings, trimmed, and a switch only when on", () => {
    expect(fieldOptions(blackhole, { drop_folder: " /drop ", watch_folder: "", save_magnet_files: false, url_base: "/x" }))
      .toEqual({ drop_folder: "/drop" });
    expect(fieldOptions(blackhole, { save_magnet_files: true, watch_folder: "/done" })).toEqual({ watch_folder: "/done", save_magnet_files: true });
    expect(fieldOptions(transmission, { url_base: "/transmission/", drop_folder: "/drop" })).toEqual({ url_base: "/transmission/" });
  });

  it("round-trips a stored client's settings", () => {
    expect(fieldOptions(blackhole, fieldValues({ drop_folder: "/a", watch_folder: "/b", save_magnet_files: true })))
      .toEqual({ drop_folder: "/a", watch_folder: "/b", save_magnet_files: true });
  });
});

describe("missingFields", () => {
  it("names the required settings left empty", () => {
    expect(missingFields(blackhole, { drop_folder: "/drop", watch_folder: "  " })).toEqual(["Watch folder"]);
    expect(missingFields(transmission, {})).toEqual([]);
  });
});

describe("kindSecret", () => {
  it("joins a user name only for a kind that signs in with one, and keeps nothing for a kind without a secret", () => {
    expect(kindSecret(transmission, "me", "pw")).toBe("me:pw");
    expect(kindSecret({ ...transmission, user_name: false, secret_label: "API key" }, "me", "key")).toBe("key");
    expect(kindSecret(blackhole, "me", "pw")).toBe("");
  });
});

describe("newLabel", () => {
  it("lower-cases a word and keeps an acronym", () => {
    expect(newLabel("Password")).toBe("New password");
    expect(newLabel("API key")).toBe("New API key");
    expect(newLabel("Web UI password")).toBe("New web UI password");
  });
});
