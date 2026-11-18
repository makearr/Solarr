import type { components } from "@/api/schema";

type HealthCheck = components["schemas"]["HealthCheck"];

/** The message, with the retry time of a self-clearing check formatted in the viewer's zone. */
export function healthMessage(check: HealthCheck): string {
  if (check.until == null) return check.message;
  const time = new Date(check.until).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
  return `${check.message} Tried again at ${time}.`;
}
