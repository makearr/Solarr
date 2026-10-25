import type { components } from "@/api/schema";
import { joinCredentials } from "@/lib/downloadCredentials";

type DownloaderKind = components["schemas"]["DownloaderKind"];
type DownloaderOptions = components["schemas"]["DownloaderOptions"];
export type FieldKey = components["schemas"]["DownloaderField"]["key"];

/** What the person typed into a kind's own settings, by key. */
export type FieldValues = Partial<Record<FieldKey, string | boolean>>;

/** A kind's own settings as options: text trimmed and left out when empty, a switch only when on. */
export function fieldOptions(kind: DownloaderKind | undefined, values: FieldValues): DownloaderOptions {
  const options: DownloaderOptions = {};
  for (const field of kind?.fields ?? []) {
    const value = values[field.key];
    if (field.kind === "bool") {
      if (value === true) options.save_magnet_files = true;
      continue;
    }
    const text = typeof value === "string" ? value.trim() : "";
    if (text === "") continue;
    if (field.key === "url_base") options.url_base = text;
    if (field.key === "drop_folder") options.drop_folder = text;
    if (field.key === "watch_folder") options.watch_folder = text;
  }
  return options;
}

/** A stored client's own settings, back into the form's values. */
export function fieldValues(options: DownloaderOptions | undefined): FieldValues {
  return {
    url_base: options?.url_base ?? "",
    drop_folder: options?.drop_folder ?? "",
    watch_folder: options?.watch_folder ?? "",
    save_magnet_files: options?.save_magnet_files ?? false,
  };
}

/** The required settings still empty, by label: a kind cannot be added without them. */
export function missingFields(kind: DownloaderKind | undefined, values: FieldValues): string[] {
  const filled = fieldOptions(kind, values);
  return (kind?.fields ?? []).filter((field) => field.required && filled[field.key] === undefined).map((field) => field.label);
}

/** The one secret a kind is stored with: "user:password" for a kind that signs in with a user name. */
export function kindSecret(kind: DownloaderKind | undefined, username: string, secret: string): string {
  if (!kind?.secret_label) return "";
  return kind.user_name ? joinCredentials(username, secret) : secret;
}

/** "New password", "New API key": a label after "New", lower case unless it starts with an acronym. */
export function newLabel(label: string): string {
  const first = label.split(" ")[0] ?? "";
  return `New ${first === first.toUpperCase() ? label : label.charAt(0).toLowerCase() + label.slice(1)}`;
}
