import type { FilterField } from "@/list/customFilter";

export const STATUS_CHOICES: readonly (readonly [string, string])[] = [["MISSING", "Missing"], ["PARTIAL", "Partial"], ["COMPLETE", "Complete"], ["UPGRADABLE", "Upgradable"]];

/** What a custom filter may name at each level; the server owns the meaning of every field. */
export const LEVEL_FILTER_FIELDS: Record<string, readonly FilterField[]> = {
  artists: [
    { key: "name", label: "Name", kind: "text" },
    { key: "country", label: "Country", kind: "text" },
    { key: "monitored", label: "Monitored", kind: "bool" },
    { key: "albums_wanted", label: "Monitored albums", kind: "number" },
    { key: "albums_complete", label: "Complete albums", kind: "number" },
  ],
  albums: [
    { key: "title", label: "Title", kind: "text" },
    { key: "artist", label: "Artist", kind: "text" },
    { key: "year", label: "Year", kind: "number" },
    { key: "genre", label: "Genre", kind: "text" },
    { key: "tag", label: "Tag", kind: "text" },
    { key: "status", label: "Status", kind: "enum", choices: STATUS_CHOICES },
    { key: "monitored", label: "Monitored", kind: "bool" },
    { key: "on_disk", label: "Has files", kind: "bool" },
    { key: "verified", label: "Identified", kind: "bool" },
    { key: "size_mb", label: "Size on disk (MB)", kind: "number" },
  ],
};
