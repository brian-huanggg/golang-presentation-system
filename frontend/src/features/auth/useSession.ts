import { useQuery } from "@tanstack/react-query";
import { ApiError, api, unwrap } from "../../api/client";
import type { components } from "../../api/generated/schema";

export type SessionUser = components["schemas"]["SessionUser"];

/** Shared by every consumer so login, logout and Layout see one source of truth. */
export const SESSION_KEY = ["session"] as const;

/**
 * The logged-in user, or null for a visitor. A 401 is an answer ("not logged in"),
 * not a failure: it is what an expired session (AUTH-005) looks like to the SPA.
 */
export async function fetchSession(): Promise<SessionUser | null> {
  const result = await api.GET("/auth/me");
  if (result.response.status === 401) return null;
  return unwrap(result);
}

export function useSession() {
  return useQuery({ queryKey: SESSION_KEY, queryFn: fetchSession });
}

export { ApiError };
