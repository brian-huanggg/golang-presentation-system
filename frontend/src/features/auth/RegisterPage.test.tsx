import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { api } from "../../api/client";
import i18n from "../../i18n";
import { Providers } from "../../app/providers";
import { createQueryClient } from "../../app/queryClient";
import { routes } from "../../app/routes";
import { toRequest, validateRegisterForm, type RegisterForm } from "./validation";

const good: RegisterForm = {
  employeeId: "123456",
  fullName: " 王小明 ",
  department: "TSID",
  password: "S3cret-pass!",
  preferredLanguage: "zh-TW",
  email: "",
};

describe("validateRegisterForm", () => {
  it("accepts a complete form without email", () => {
    expect(validateRegisterForm(good)).toEqual({});
  });

  it.each(["12345", "1234567", "12a456", "12 456", " 12345", "12-456", "１２３４５６"])(
    "rejects employee id %j (REG-002)",
    (employeeId) => {
      expect(validateRegisterForm({ ...good, employeeId }).employeeId).toBe("invalid_format");
    },
  );

  it("requires every mandatory field and format-checks a given email", () => {
    const errs = validateRegisterForm({ ...good, employeeId: "", fullName: " ", department: "", password: "", preferredLanguage: "", email: "nope" });
    expect(Object.keys(errs).sort()).toEqual(
      ["department", "email", "employeeId", "fullName", "password", "preferredLanguage"],
    );
  });
});

it("toRequest trims the name and omits an empty email", () => {
  expect(toRequest(good)).toEqual({
    employeeId: "123456",
    fullName: "王小明",
    department: "TSID",
    password: "S3cret-pass!",
    preferredLanguage: "zh-TW",
  });
});

describe("RegisterPage", () => {
  function renderPage() {
    const router = createMemoryRouter(routes, { initialEntries: ["/register"] });
    return render(
      <Providers client={createQueryClient()}>
        <RouterProvider router={router} />
      </Providers>,
    );
  }

  // Node's Request rejects the client's relative base URL, so stub the typed client call.
  function mockPost(status: number, body: unknown) {
    const ok = status < 300;
    const spy = vi.spyOn(api, "POST").mockResolvedValue({
      ...(ok ? { data: body } : { error: body }),
      response: new Response(null, { status }),
    } as never);
    return spy;
  }

  async function fillAndSubmit(employeeId = "123456") {
    const user = userEvent.setup();
    await user.type(screen.getByLabelText(/員工編號/), employeeId);
    await user.type(screen.getByLabelText(/姓名/), "王小明");
    await user.click(screen.getByRole("combobox", { name: /部門/ }));
    await user.click(await screen.findByRole("option", { name: "TSID" }));
    await user.type(screen.getByLabelText(/密碼/), "S3cret-pass!");
    await user.click(screen.getByRole("button", { name: "註冊" }));
  }

  beforeEach(() => i18n.changeLanguage("zh-TW"));
  afterEach(() => vi.restoreAllMocks());

  it("blocks a bad Employee ID without calling the API", async () => {
    const spy = mockPost(201, {});
    renderPage();
    await fillAndSubmit("12345");
    expect(await screen.findByText("格式不正確。")).toBeInTheDocument();
    expect(spy).not.toHaveBeenCalled();
  });

  it("registers and shows success", async () => {
    const spy = mockPost(201, { employeeId: "123456", fullName: "王小明", department: "TSID", preferredLanguage: "zh-TW" });
    renderPage();
    await fillAndSubmit();
    expect(await screen.findByText("註冊完成。")).toBeInTheDocument();
    expect(spy).toHaveBeenCalledWith("/auth/register", {
      body: {
        employeeId: "123456", fullName: "王小明", department: "TSID", password: "S3cret-pass!", preferredLanguage: "zh-TW",
      },
    });
  });

  it("tells the visitor the ID is already registered (REG-009)", async () => {
    mockPost(409, { title: "Conflict", status: 409, code: "employee_id_taken" });
    renderPage();
    await fillAndSubmit();
    expect(await screen.findByText("此員工編號已註冊。")).toBeInTheDocument();
  });

  it("shows the deadline message after the deadline (REG-006)", async () => {
    mockPost(403, { title: "Forbidden", status: 403, code: "deadline_passed" });
    renderPage();
    await fillAndSubmit();
    expect(await screen.findByText("已超過註冊截止時間。")).toBeInTheDocument();
  });

  it("maps server field errors onto the fields", async () => {
    mockPost(400, { title: "Bad Request", status: 400, code: "validation_failed", errors: [{ field: "email", code: "invalid_format" }] });
    renderPage();
    await fillAndSubmit();
    await waitFor(() => expect(screen.getByText("格式不正確。")).toBeInTheDocument());
  });

  it("renders in English when the UI language is English", async () => {
    await i18n.changeLanguage("en");
    renderPage();
    expect(screen.getByRole("heading", { name: "Register" })).toBeInTheDocument();
    expect(screen.getByLabelText(/Employee ID/)).toBeInTheDocument();
  });
});
