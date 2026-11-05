import { ApiError, unwrap } from "./client";

it("unwrap returns data on success", () => {
  expect(unwrap({ data: { status: "ok" }, response: new Response() })).toEqual({ status: "ok" });
});

it("unwrap throws ApiError carrying the problem", () => {
  const problem = { type: "about:blank", title: "Nope", status: 403, code: "FORBIDDEN" };
  try {
    unwrap({ error: problem, response: new Response(null, { status: 403 }) });
    expect.unreachable();
  } catch (e) {
    expect(e).toBeInstanceOf(ApiError);
    expect((e as ApiError).problem?.code).toBe("FORBIDDEN");
  }
});

it("echoes the CSRF cookie on state-changing requests only", async () => {
  const { api, CSRF_COOKIE, CSRF_HEADER } = await import("./client");
  document.cookie = `${CSRF_COOKIE}=abc%20123`;
  const BASE = "http://localhost/api/v1"; // Node Request rejects relative URLs.
  const seen: Request[] = [];
  const fetch = async (req: Request) => {
    seen.push(req);
    return new Response("{}", { headers: { "content-type": "application/json" } });
  };
  await api.GET("/health", { fetch, baseUrl: BASE });
  await api.POST("/health" as never, { fetch, baseUrl: BASE } as never);
  expect(seen[0]!.headers.get(CSRF_HEADER)).toBeNull();
  expect(seen[1]!.headers.get(CSRF_HEADER)).toBe("abc 123");
});
