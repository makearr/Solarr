// Mirrored into localStorage for the pre-paint script in index.html.
// See documentation/web-interface.md.
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
    // Denied storage loses only the next load's pre-paint hint.
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
