/** The age a rating admits, as the one scale the interface shows: 0, 6, 12, 16 or 18 (adult content is always 18). */
export const AGE_STEPS = [0, 6, 12, 16, 18] as const;

/** The step a work is shown at; undefined when its rating is not understood and it is not adult. */
export function ageStep(age: number | undefined, adult: boolean): number | undefined {
  if (adult) return 18;
  if (age === undefined || age < 0) return undefined;
  return [...AGE_STEPS].reverse().find((step) => age >= step) ?? 0;
}

/** "12+", or "All ages" for 0. */
export function ageLabel(step: number): string {
  return step === 0 ? "All ages" : `${step}+`;
}
