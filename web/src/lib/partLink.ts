/** The one link every list uses for an episode (or track): its work page, opened on that part. */
export function partLink(workId: number, mediaRef: string, categoryId?: string) {
  const search = { ref: mediaRef } as never;
  return categoryId
    ? ({ to: "/library/$categoryId/works/$workId/$tab", params: { categoryId, workId: String(workId), tab: "overview" }, search } as const)
    : ({ to: "/works/$id/$tab", params: { id: String(workId), tab: "overview" }, search } as const);
}
