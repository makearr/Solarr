/** The group a TV season is wanted through, until the TV screens that name seasons give way to the shared ones. */
export function seasonGroup(season: number): string {
  return `s${String(season).padStart(2, "0")}`;
}
