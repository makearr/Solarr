/** Whether a key press is the search shortcut: a bare "/" typed outside any field. */
export function isSearchShortcut(event: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "altKey" | "target">): boolean {
  if (event.key !== "/" || event.ctrlKey || event.metaKey || event.altKey) return false;
  const target = event.target as HTMLElement | null;
  if (target === null || typeof target.tagName !== "string") return true;
  if (target.isContentEditable) return false;
  return !["INPUT", "TEXTAREA", "SELECT"].includes(target.tagName);
}
