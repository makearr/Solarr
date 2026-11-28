/** The ages a user's Maximum age offers; a work's raw normalised age is compared against it (TV-MA is 17). */
export const MAX_AGE_CHOICES = [0, 6, 7, 10, 12, 13, 14, 16, 17, 18] as const;

/** The exact age a work is shown at (adult content is always 18); undefined when its rating is not understood and it is not adult. */
export function shownAge(age: number | undefined, adult: boolean): number | undefined {
  if (adult) return 18;
  if (age === undefined || age < 0) return undefined;
  return age;
}

/** "14+", or "All ages" for 0. */
export function ageLabel(age: number): string {
  return age === 0 ? "All ages" : `${age}+`;
}
