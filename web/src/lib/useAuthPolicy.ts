import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { api } from "@/api/client";

/** Whether a login is required. Unauthenticated on purpose: an instance that
 *  requires none has nobody to authenticate, and the interface still has to be
 *  able to find that out. */
export function useAuthPolicy() {
  return useQuery({
    queryKey: ["auth-policy"],
    queryFn: async () => {
      const { data, error } = await api.GET("/settings/auth");
      if (error) throw error;
      return data;
    },
  });
}

/** Invalidates everything on success: turning the requirement on or off changes
 *  who every other query is answered as. */
export function useSetAuthRequired() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: async (change: { authRequired: boolean; password?: string }) => {
      const { data, error } = await api.PUT("/settings/auth", {
        body: { auth_required: change.authRequired, password: change.password },
      });
      if (error) throw error;
      return data;
    },
    onSuccess: () => queryClient.invalidateQueries(),
  });
}
