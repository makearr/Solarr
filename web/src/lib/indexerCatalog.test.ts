import { describe, expect, it } from "vitest";

import type { components } from "@/api/schema";
import {
  NO_FILTER, alreadyAdded, filterCatalog, indexerSecret, missingDownloader, missingIndexer, missingIndexerFields,
} from "@/lib/indexerCatalog";

type IndexerKind = components["schemas"]["IndexerKind"];
type Indexer = components["schemas"]["Indexer"];
type Downloader = components["schemas"]["Downloader"];
type DownloaderKind = components["schemas"]["DownloaderKind"];

const kind = (over: Partial<IndexerKind>): IndexerKind => ({
  id: "x", name: "X", description: "", impl: "newznab", protocol: "usenet", privacy: "private", language: "en-US",
  base_urls: [], media_types: ["music"], secret_optional: false, ...over,
});

const geek = kind({ id: "nzbgeek", name: "NZBgeek", base_urls: ["https://api.nzbgeek.info"], secret_label: "API key", media_types: ["music", "tv"] });
const tpb = kind({ id: "thepiratebay", name: "The Pirate Bay", impl: "thepiratebay", protocol: "torrent", privacy: "public",
  description: "the largest public tracker", base_urls: ["https://apibay.org"], media_types: ["movies"] });
const soulseek = kind({ id: "soulseek", name: "Soulseek (slskd)", impl: "soulseek", protocol: "ddl", privacy: "semi-private",
  base_urls: ["http://slskd:5030"], secret_label: "API key", companion_downloader: "slskd" });
const hdbits = kind({ id: "hdbits", name: "HDBits", impl: "hdbits", protocol: "torrent", user_label: "User name", secret_label: "Passkey" });

const indexer = (over: Partial<Indexer>): Indexer => ({
  id: 1, name: "n", impl: "newznab", base_url: "", enabled: true, priority: 25, api_key: { has_value: true },
  created_at: "", updated_at: "", ...over,
});

describe("filterCatalog", () => {
  const all = [geek, tpb, soulseek, hdbits];
  it("narrows by words in the name or description, protocol, privacy and media", () => {
    expect(filterCatalog(all, { ...NO_FILTER, text: "public tracker" })).toEqual([tpb]);
    expect(filterCatalog(all, { ...NO_FILTER, protocol: "torrent" })).toEqual([tpb, hdbits]);
    expect(filterCatalog(all, { ...NO_FILTER, privacy: "private", media: "tv" })).toEqual([geek]);
    expect(filterCatalog(all, NO_FILTER)).toEqual(all);
  });
});

describe("alreadyAdded", () => {
  it("matches the entry an indexer came from, or an older one by impl and address", () => {
    expect(alreadyAdded(geek, [indexer({ definition: "nzbgeek", base_url: "https://elsewhere" })])).toBe(true);
    expect(alreadyAdded(geek, [indexer({ definition: "nzbfinder", base_url: "https://api.nzbgeek.info" })])).toBe(false);
    expect(alreadyAdded(geek, [indexer({ base_url: "https://API.nzbgeek.info/" })])).toBe(true);
  });
});

describe("indexerSecret and missingIndexerFields", () => {
  it("joins two-part credentials and names what is missing", () => {
    expect(indexerSecret(hdbits, "me", "pk")).toBe("me:pk");
    expect(indexerSecret(geek, "ignored", "key")).toBe("key");
    expect(indexerSecret(tpb, "", "x")).toBe("");
    expect(missingIndexerFields(hdbits, "https://hdbits.org", "", "")).toEqual(["User name", "Passkey"]);
    expect(missingIndexerFields(kind({ secret_label: "API key", secret_optional: true }), "", "", "")).toEqual(["Address"]);
  });
});

describe("companions", () => {
  const slskd: DownloaderKind = {
    impl: "slskd", label: "slskd (Soulseek)", protocol: "ddl", has_address: true, user_name: false, keeps_completed: false,
    categories: false, fields: [], companion_indexer: "soulseek",
  };
  const client = (over: Partial<Downloader>): Downloader => ({
    id: 1, name: "s", impl: "slskd", base_url: "http://slskd:5030", enabled: true, protocol: "ddl",
    credentials: { has_value: true }, created_at: "", updated_at: "", ...over,
  });
  it("offers slskd for Soulseek until one is at its address, and the other way round", () => {
    expect(missingDownloader(soulseek, "http://slskd:5030", [], [slskd])).toEqual(slskd);
    expect(missingDownloader(soulseek, "http://slskd:5030/", [client({})], [slskd])).toBeUndefined();
    expect(missingDownloader(geek, "https://api.nzbgeek.info", [], [slskd])).toBeUndefined();
    expect(missingIndexer(slskd, "http://slskd:5030", [], [soulseek])).toEqual(soulseek);
    expect(missingIndexer(slskd, "http://slskd:5030", [indexer({ definition: "soulseek", base_url: "http://slskd:5030" })], [soulseek]))
      .toBeUndefined();
  });
});
