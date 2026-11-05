import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { api } from "../../api/client";
import i18n from "../../i18n";
import { Providers } from "../../app/providers";
import { createQueryClient } from "../../app/queryClient";
import { routes } from "../../app/routes";

const user = {
  employeeId: "123456",
  fullName: "王小明",
  role: "PARTICIPANT",
  department: "TSID",
  preferredLanguage: "zh-TW",
  mustChangePassword: false,
};

// Node's Request rejects the client's relative base URL, so stub the typed client calls.
function reply(status: number, body?: unknown) {
  return {
    ...(status < 300 ? { data: body } : { error: body }),
    response: new Response(null, { status }),
  } as never;
}

/** `me` answers 401 (visitor) until `loggedIn.value` flips, like the real server. */
function stubApi(opts: { login?: ReturnType<typeof reply> } = {}) {
  const state = { loggedIn: false };
  const get = vi.spyOn(api, "GET").mockImplementation((async () =>
    state.loggedIn ? reply(200, user) : reply(401, { status: 401, code: "unauthenticated" })) as never);
  const post = vi.spyOn(api, "POST").mockImplementation((async (path: string) => {
    if (path === "/auth/login") {
      const r = opts.login ?? reply(200, user);
      state.loggedIn = true;
      return r;
    }
    state.loggedIn = false; // /auth/logout
    return reply(204);
  }) as never);
  return { get, post, state };
}

function renderAt(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  render(
    <Providers client={createQueryClient()}>
      <RouterProvider router={router} />
    </Providers>,
  );
  return router;
}

async function submit(id: string, password: string) {
  const u = userEvent.setup();
  await u.type(screen.getByLabelText(/員工編號/), id);
  await u.type(screen.getByLabelText(/密碼/), password);
  await u.click(screen.getByRole("button", { name: "登入" }));
}

beforeEach(() => i18n.changeLanguage("zh-TW"));
afterEach(() => vi.restoreAllMocks());

describe("LoginPage", () => {
  it("AUTH-001 rejects an email address without calling the API", async () => {
    const { post } = stubApi();
    renderAt("/login");
    await submit("ming@example.com", "S3cret-pass!");
    expect(await screen.findByText(/不可使用 Email 登入/)).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("requires a password", async () => {
    const { post } = stubApi();
    renderAt("/login");
    const u = userEvent.setup();
    await u.type(screen.getByLabelText(/員工編號/), "123456");
    await u.click(screen.getByRole("button", { name: "登入" }));
    expect(await screen.findByText("此欄位為必填。")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("logs in, shows the user and goes home", async () => {
    const { post } = stubApi();
    const router = renderAt("/login");
    await submit("123456", "S3cret-pass!");
    expect(await screen.findByText("王小明")).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/auth/login", { body: { employeeId: "123456", password: "S3cret-pass!" } });
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
  });

  it("I18N-002 applies the saved preferred language after login", async () => {
    stubApi({ login: reply(200, { ...user, preferredLanguage: "en" }) });
    renderAt("/login");
    await submit("123456", "S3cret-pass!");
    expect(await screen.findByRole("button", { name: "Log out" })).toBeInTheDocument();
  });

  it("AUTH-003 shows one generic message for any credential failure", async () => {
    stubApi({ login: reply(401, { status: 401, code: "invalid_credentials", detail: "Invalid Employee ID or password" }) });
    renderAt("/login");
    await submit("123456", "wrong");
    expect(await screen.findByText("員工編號或密碼錯誤。")).toBeInTheDocument();
  });

  it("shows a generic error for an unexpected failure", async () => {
    stubApi({ login: reply(500, { status: 500, code: "internal_error" }) });
    renderAt("/login");
    await submit("123456", "S3cret-pass!");
    expect(await screen.findByText("發生錯誤，請稍後再試。")).toBeInTheDocument();
  });

  it("renders in English", async () => {
    await i18n.changeLanguage("en");
    stubApi();
    renderAt("/login");
    expect(screen.getByRole("heading", { name: "Log in" })).toBeInTheDocument();
  });
});

describe("session in the layout", () => {
  it("a visitor sees login and register links, no logout", async () => {
    stubApi();
    renderAt("/");
    expect(await screen.findByRole("link", { name: "登入" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "登出" })).not.toBeInTheDocument();
  });

  it("AUTH-006 logout calls the API, forgets the user and returns to login", async () => {
    const { post, state } = stubApi();
    state.loggedIn = true;
    const router = renderAt("/");
    await userEvent.click(await screen.findByRole("button", { name: "登出" }));
    await waitFor(() => expect(post).toHaveBeenCalledWith("/auth/logout"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(screen.queryByText("王小明")).not.toBeInTheDocument();
  });

  it("AUTH-005 an expired session (401) renders as a visitor", async () => {
    stubApi(); // /auth/me answers 401
    renderAt("/");
    expect(await screen.findByRole("link", { name: "登入" })).toBeInTheDocument();
  });
});
