import type { components } from "@/api/schema";

type IndexerKind = components["schemas"]["IndexerKind"];
type Indexer = components["schemas"]["Indexer"];
type Downloader = components["schemas"]["Downloader"];
type DownloaderKind = components["schemas"]["DownloaderKind"];

/** What the add list is narrowed by; empty means any. */
export interface CatalogFilter {
  text: string;
  protocol: "" | IndexerKind["protocol"];
  privacy: "" | IndexerKind["privacy"];
  media: "" | IndexerKind["media_types"][number];
}

export const NO_FILTER: CatalogFilter = { text: "", protocol: "", privacy: "", media: "" };

/** The catalog entries a filter keeps, by name and description, in the catalog's order. */
export function filterCatalog(kinds: IndexerKind[], filter: CatalogFilter): IndexerKind[] {
  const words = filter.text.trim().toLowerCase().split(/\s+/).filter(Boolean);
  return kinds.filter((kind) => {
    const haystack = `${kind.name} ${kind.description} ${kind.id}`.toLowerCase();
    return words.every((word) => haystack.includes(word)) &&
      (filter.protocol === "" || kind.protocol === filter.protocol) &&
      (filter.privacy === "" || kind.privacy === filter.privacy) &&
      (filter.media === "" || kind.media_types.includes(filter.media));
  });
}

const sameAddress = (a: string, b: string) => a.replace(/\/+$/, "").toLowerCase() === b.replace(/\/+$/, "").toLowerCase();

/** An entry already added: one indexer made from it, or (before the catalog) of its impl at one of its addresses. */
export function alreadyAdded(kind: IndexerKind, indexers: Indexer[]): boolean {
  return indexers.some((indexer) =>
    indexer.definition !== undefined
      ? indexer.definition === kind.id
      : indexer.impl === kind.impl && kind.base_urls.some((url) => sameAddress(url, indexer.base_url)));
}

/** The secret an entry is stored with: "user:secret" for one with two parts, nothing when both are empty. */
export function indexerSecret(kind: IndexerKind, user: string, secret: string): string {
  if (!kind.secret_label) return "";
  if (!kind.user_label) return secret;
  return user === "" && secret === "" ? "" : `${user.trim()}:${secret}`;
}

/** What an entry still needs before it can be added, by label. */
export function missingIndexerFields(kind: IndexerKind, address: string, user: string, secret: string): string[] {
  const missing: string[] = [];
  if (address.trim() === "") missing.push("Address");
  if (kind.user_label && user.trim() === "") missing.push(kind.user_label);
  if (kind.secret_label && !kind.secret_optional && secret === "") missing.push(kind.secret_label);
  return missing;
}

/** The download client an indexer's releases need and that is not there yet, at its address: slskd for Soulseek. */
export function missingDownloader(
  kind: IndexerKind | undefined, address: string, downloaders: Downloader[], clients: DownloaderKind[],
): DownloaderKind | undefined {
  const impl = kind?.companion_downloader;
  if (!impl || downloaders.some((client) => client.impl === impl && sameAddress(client.base_url, address))) return undefined;
  return clients.find((client) => client.impl === impl);
}

/** The indexer a download client's releases are searched by and that is not there yet, at its address. */
export function missingIndexer(
  client: DownloaderKind | undefined, address: string, indexers: Indexer[], kinds: IndexerKind[],
): IndexerKind | undefined {
  const id = client?.companion_indexer;
  if (!id || indexers.some((indexer) => indexer.definition === id && sameAddress(indexer.base_url, address))) return undefined;
  return kinds.find((kind) => kind.id === id);
}
