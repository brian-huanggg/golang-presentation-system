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

function reply(status: number, body?: unknown) {
  return {
    ...(status < 300 ? { data: body } : { error: body }),
    response: new Response(null, { status }),
  } as never;
}

function stubApi(opts: { loggedIn?: boolean; password?: ReturnType<typeof reply>; patch?: ReturnType<typeof reply> } = {}) {
  const loggedIn = opts.loggedIn ?? true;
  const get = vi.spyOn(api, "GET").mockImplementation((async () =>
    loggedIn ? reply(200, user) : reply(401, { status: 401, code: "unauthenticated" })) as never);
  const post = vi.spyOn(api, "POST").mockImplementation((async () => opts.password ?? reply(204)) as never);
  const patch = vi.spyOn(api, "PATCH").mockImplementation((async (_p: string, init: { body: { preferredLanguage: string } }) =>
    opts.patch ?? reply(200, { ...user, preferredLanguage: init.body.preferredLanguage })) as never);
  return { get, post, patch };
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

async function fill(current: string, next: string, confirm: string) {
  const u = userEvent.setup();
  // userEvent cannot type "", so an empty value simply leaves the field untouched.
  const type = async (el: HTMLElement, value: string) => value && (await u.type(el, value));
  await type(await screen.findByLabelText(/目前密碼/), current);
  await type(screen.getByLabelText(/^新密碼/), next);
  await type(screen.getByLabelText(/確認新密碼/), confirm);
  await u.click(screen.getByRole("button", { name: "變更密碼" }));
}

beforeEach(() => i18n.changeLanguage("zh-TW"));
afterEach(() => vi.restoreAllMocks());

describe("ChangePasswordPage", () => {
  it("REG-007 sends the current and new password", async () => {
    const { post } = stubApi();
    renderAt("/account/password");
    await fill("Old-secret-1!", "N3w-secret-pw#Q", "N3w-secret-pw#Q");
    expect(await screen.findByText(/其他登入階段已登出/)).toBeInTheDocument();
    expect(post).toHaveBeenCalledWith("/auth/password", {
      body: { currentPassword: "Old-secret-1!", newPassword: "N3w-secret-pw#Q" },
    });
  });

  it("REG-007 requires all fields and a matching confirmation without calling the API", async () => {
    const { post } = stubApi();
    renderAt("/account/password");
    await fill("", "N3w-secret-pw#Q", "different");
    expect(await screen.findByText("此欄位為必填。")).toBeInTheDocument();
    expect(screen.getByText("兩次輸入的密碼不一致。")).toBeInTheDocument();
    expect(post).not.toHaveBeenCalled();
  });

  it("REG-007 shows the server's wrong-current-password error on that field", async () => {
    stubApi({
      password: reply(400, { status: 400, code: "validation_failed", errors: [{ field: "currentPassword", code: "password_incorrect" }] }),
    });
    renderAt("/account/password");
    await fill("Wrong-pass-9!", "N3w-secret-pw#Q", "N3w-secret-pw#Q");
    expect(await screen.findByText("目前密碼不正確。")).toBeInTheDocument();
  });

  it("REG-005 shows a policy violation of the new password", async () => {
    stubApi({
      password: reply(400, { status: 400, code: "validation_failed", errors: [{ field: "newPassword", code: "password_sequence" }] }),
    });
    renderAt("/account/password");
    await fill("Old-secret-1!", "Abcdef-Xyz-123!", "Abcdef-Xyz-123!");
    expect(await screen.findByText(/不可有 3 個以上連續的字母或數字/)).toBeInTheDocument();
  });

  it("shows a retry message on a conflict and a generic one otherwise", async () => {
    stubApi({ password: reply(409, { status: 409, code: "conflict" }) });
    renderAt("/account/password");
    await fill("Old-secret-1!", "N3w-secret-pw#Q", "N3w-secret-pw#Q");
    expect(await screen.findByText(/請再試一次/)).toBeInTheDocument();
  });

  it("shows a generic message for an unexpected failure", async () => {
    stubApi({ password: reply(500, { status: 500, code: "internal_error" }) });
    renderAt("/account/password");
    await fill("Old-secret-1!", "N3w-secret-pw#Q", "N3w-secret-pw#Q");
    expect(await screen.findByText("發生錯誤，請稍後再試。")).toBeInTheDocument();
  });

  it("sends a visitor to the login page", async () => {
    stubApi({ loggedIn: false });
    const router = renderAt("/account/password");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("is reachable from the layout link", async () => {
    stubApi();
    const router = renderAt("/");
    await userEvent.click(await screen.findByRole("link", { name: "變更密碼" }));
    await waitFor(() => expect(router.state.location.pathname).toBe("/account/password"));
  });
});

describe("LanguageSwitcher", () => {
  async function pick(name: string) {
    await userEvent.click(await screen.findByRole("combobox", { name: /語言|Language/ }));
    await userEvent.click(await screen.findByRole("option", { name }));
  }

  it("I18N-002 saves the choice of a logged-in user", async () => {
    const { patch } = stubApi();
    renderAt("/");
    await screen.findByText("王小明");
    await pick("English");
    await waitFor(() => expect(patch).toHaveBeenCalledWith("/auth/me", { body: { preferredLanguage: "en" } }));
    expect(await screen.findByRole("heading", { name: "ESG Award" })).toBeInTheDocument();
  });

  it("I18N-002 does not call the API for a visitor", async () => {
    const { patch } = stubApi({ loggedIn: false });
    renderAt("/");
    await pick("English");
    expect(await screen.findByRole("heading", { name: "ESG Award" })).toBeInTheDocument();
    expect(patch).not.toHaveBeenCalled();
  });

  it("I18N-002 still switches the UI when saving fails", async () => {
    stubApi({ patch: reply(500, { status: 500, code: "internal_error" }) });
    renderAt("/");
    await screen.findByText("王小明");
    await pick("English");
    expect(await screen.findByRole("heading", { name: "ESG Award" })).toBeInTheDocument();
  });
});
