import type { LinkProps } from "@tanstack/react-router";

/** Where a work opens: its page inside the library it is listed in. */
export function workLink(categoryId: number, workId: number): LinkProps {
  return { to: "/library/$categoryId/works/$workId", params: { categoryId: String(categoryId), workId: String(workId) } };
}
