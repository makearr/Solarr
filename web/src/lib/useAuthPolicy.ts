import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api, ok } from "@/api/client";

/** Whether a login is required. Unauthenticated on purpose: an instance that
 *  requires none has nobody to authenticate, and the interface still has to be
 *  able to find that out. */
export function useAuthPolicy() {
  return useQuery({
    queryKey: ["auth-policy"],
    queryFn: () => ok(api.GET("/settings/auth")),
  });
}

/** Invalidates everything on success: turning the requirement on or off changes
 *  who every other query is answered as. */
export function useSetAuthRequired() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (change: { authRequired: boolean; password?: string }) => ok(api.PUT("/settings/auth", {
      body: { auth_required: change.authRequired, password: change.password },
    })),
    onSuccess: () => queryClient.invalidateQueries(),
  });
}
