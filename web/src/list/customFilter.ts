// Custom filters (Sonarr's and Lidarr's "custom filter"): a list of conditions that must all hold, built and saved by a
// person. The same JSON travels in the URL (`where`) and to the server for lists the server pages.

export type FilterKind = "text" | "number" | "bool" | "enum";

export interface FilterField {
  key: string;
  label: string;
  kind: FilterKind;
  /** The values of an enum, as [value, label]. */
  choices?: readonly (readonly [string, string])[];
}

export interface Condition {
  field: string;
  op: string;
  value: string;
}

export interface SavedFilter {
  id: string;
  name: string;
  conditions: Condition[];
}

export const OPERATORS: Record<FilterKind, readonly (readonly [string, string])[]> = {
  text: [["contains", "contains"], ["not_contains", "does not contain"], ["is", "is"], ["is_not", "is not"], ["starts_with", "starts with"]],
  number: [["eq", "="], ["ne", "≠"], ["gt", ">"], ["ge", "≥"], ["lt", "<"], ["le", "≤"]],
  bool: [["is", "is"]],
  enum: [["is", "is"], ["is_not", "is not"]],
};

/** The conditions a `where` parameter carries; anything unreadable is no filter. */
export function parseWhere(raw: string | undefined): Condition[] {
  if (!raw) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (entry): entry is Condition => typeof entry === "object" && entry !== null && typeof entry.field === "string" && typeof entry.op === "string" && typeof entry.value === "string",
    );
  } catch {
    return [];
  }
}

export function toWhere(conditions: readonly Condition[]): string {
  return conditions.length === 0 ? "" : JSON.stringify(conditions.map(({ field, op, value }) => ({ field, op, value })));
}

/** A condition that holds of a value the list reads from an item; a text field may have several values (genres). */
export function holds(condition: Condition, kind: FilterKind, values: readonly (string | number | boolean | undefined | null)[]): boolean {
  const have = values.filter((value): value is string | number | boolean => value !== undefined && value !== null);
  switch (kind) {
    case "text": {
      const wanted = condition.value.toLowerCase();
      const texts = have.map((value) => String(value).toLowerCase());
      switch (condition.op) {
        case "contains": return texts.some((text) => text.includes(wanted));
        case "not_contains": return !texts.some((text) => text.includes(wanted));
        case "is": return texts.some((text) => text === wanted);
        case "is_not": return !texts.some((text) => text === wanted);
        case "starts_with": return texts.some((text) => text.startsWith(wanted));
        default: return true;
      }
    }
    case "number": {
      const wanted = Number(condition.value);
      const number = have.length > 0 ? Number(have[0]) : 0;
      switch (condition.op) {
        case "eq": return number === wanted;
        case "ne": return number !== wanted;
        case "gt": return number > wanted;
        case "ge": return number >= wanted;
        case "lt": return number < wanted;
        case "le": return number <= wanted;
        default: return true;
      }
    }
    case "bool":
      return (have.length > 0 && Boolean(have[0])) === (condition.value === "true");
    case "enum": {
      const is = have.some((value) => String(value) === condition.value);
      return condition.op === "is_not" ? !is : is;
    }
  }
}

/** Whether an item passes every condition; `read` gives the values of one field for it. */
export function passes(
  conditions: readonly Condition[],
  fields: readonly FilterField[],
  read: (field: string) => readonly (string | number | boolean | undefined | null)[],
): boolean {
  return conditions.every((condition) => {
    const field = fields.find((entry) => entry.key === condition.field);
    return field ? holds(condition, field.kind, read(condition.field)) : true;
  });
}

export function describe(condition: Condition, fields: readonly FilterField[]): string {
  const field = fields.find((entry) => entry.key === condition.field);
  if (!field) return `${condition.field} ${condition.op} ${condition.value}`;
  const op = OPERATORS[field.kind].find(([key]) => key === condition.op)?.[1] ?? condition.op;
  const value = field.kind === "enum" ? (field.choices?.find(([key]) => key === condition.value)?.[1] ?? condition.value) : field.kind === "bool" ? (condition.value === "true" ? "yes" : "no") : condition.value;
  return `${field.label} ${op} ${value}`;
}
