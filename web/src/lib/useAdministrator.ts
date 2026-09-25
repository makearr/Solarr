import { useQuery } from "@tanstack/react-query";

import { api } from "@/api/client";

/** Whether the caller is an administrator, the one role every configuration write needs.
 *  Reads the shared ["me"] query; open while it loads, as useTier is -- the server refuses anyway. */
export function useIsAdministrator(): boolean {
  const me = useQuery({
    queryKey: ["me"],
    queryFn: async () => {
      const { data, error } = await api.GET("/me");
      if (error) throw error;
      return data;
    },
  });
  return me.data === undefined || me.data.role === "admin";
}

/** Pages that configure the instance or a library: only an administrator can change anything on
 *  them, so nobody else is shown them (found 2026-09-24). A user's own settings stay open. */
export function isAdministratorPath(path: string): boolean {
  if (path.startsWith("/settings/")) return path !== "/settings/user";
  return (
    path.startsWith("/storage") ||
    path.startsWith("/connections") ||
    path.startsWith("/quality-profiles") ||
    /^\/library\/[^/]+\/(config(\/|$)|import$)/.test(path)
  );
}
