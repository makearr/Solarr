// The theme lives on <html> and is mirrored into localStorage so the inline
// script in index.html can apply it before first paint.
//
// Browser storage is deliberate here and does not contradict section 21.2's rule
// that view state is server-side: this is a pre-authentication paint decision,
// needed before any request can be made. The user's stored preference remains
// authoritative once loaded.
export type Theme = "light" | "dark" | "system";

const STORAGE_KEY = "medianexus.theme";

export function resolve(theme: Theme): "light" | "dark" {
  if (theme !== "system") return theme;
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

export function apply(theme: Theme): void {
  document.documentElement.classList.toggle("dark", resolve(theme) === "dark");
  try {
    if (theme === "system") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, theme);
  } catch {
    // Storage can be denied. The class is already set, so this run is correct;
    // only the pre-paint hint for the next load is lost.
  }
}

export function stored(): Theme {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    return value === "light" || value === "dark" ? value : "system";
  } catch {
    return "system";
  }
}
