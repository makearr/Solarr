import type { LinkProps } from "@tanstack/react-router";

/** Where a work opens: the one place the web still names a media type, until works share one page (R1.6). */
export function workLink(mediaType: string, categoryId: number, workId: number): LinkProps {
  if (mediaType === "tv") {
    return { to: "/library/$categoryId/series/$seriesId", params: { categoryId: String(categoryId), seriesId: String(workId) } };
  }
  return { to: "/works/$id", params: { id: String(workId) } };
}
