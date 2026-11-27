import { useQuery } from "@tanstack/react-query";

import { api, ok } from "@/api/client";

/** Whether the interface lists collections (Settings, General); the data is collected either way. */
export function useShowCollections(): boolean {
  const general = useQuery({ queryKey: ["general-settings"], queryFn: () => ok(api.GET("/settings/general")) });
  return general.data?.show_collections ?? false;
}
