import createClient, { type Middleware } from "openapi-fetch";
import type { components, paths } from "./generated/schema";

export type Problem = components["schemas"]["Problem"];

/** Thrown for non-2xx responses; carries the RFC 9457 body (I18N-003: English text). */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly problem?: Problem,
  ) {
    super(problem?.title ?? `HTTP ${status}`);
  }
}

const SAFE_METHODS = new Set(["GET", "HEAD", "OPTIONS"]);
export const CSRF_COOKIE = "csrf_token";
export const CSRF_HEADER = "X-CSRF-Token";

function readCookie(name: string): string | undefined {
  return document.cookie
    .split("; ")
    .find((c) => c.startsWith(`${name}=`))
    ?.slice(name.length + 1);
}

// AUTH-009: echo the CSRF cookie in a header on every state-changing request.
const csrf: Middleware = {
  onRequest({ request }) {
    if (!SAFE_METHODS.has(request.method)) {
      const token = readCookie(CSRF_COOKIE);
      if (token) request.headers.set(CSRF_HEADER, decodeURIComponent(token));
    }
    return request;
  },
};

export const api = createClient<paths>({ baseUrl: "/api/v1", credentials: "same-origin" });
api.use(csrf);

/** Unwraps an openapi-fetch result, turning Problem Details into an ApiError. */
export function unwrap<T>(result: { data?: T; error?: unknown; response: Response }): T {
  if (result.error !== undefined || result.data === undefined) {
    throw new ApiError(result.response.status, result.error as Problem | undefined);
  }
  return result.data;
}
