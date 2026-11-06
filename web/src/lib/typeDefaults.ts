/** The list with the item at `index` moved by `delta` places; the list itself when that leaves it. */
export function moveItem<T>(list: readonly T[], index: number, delta: number): T[] {
  const target = index + delta;
  const moving = list[index];
  const displaced = list[target];
  if (moving === undefined || displaced === undefined) return [...list];
  const next = [...list];
  next[index] = displaced;
  next[target] = moving;
  return next;
}

/** What a picker's "default" entry says: the inherited choice by name, else that the type sets none. */
export function inheritedLabel(inherited: boolean, name: string | undefined, none: string): string {
  return inherited && name ? `Type default: ${name}` : none;
}
