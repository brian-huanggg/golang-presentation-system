import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import "../i18n";
import i18n from "../i18n";
import { Providers } from "./providers";
import { createQueryClient } from "./queryClient";
import { routes } from "./routes";

function renderAt(path: string) {
  const router = createMemoryRouter(routes, { initialEntries: [path] });
  return render(
    <Providers client={createQueryClient()}>
      <RouterProvider router={router} />
    </Providers>,
  );
}

beforeEach(() => i18n.changeLanguage("zh-TW"));

it("renders the home route in zh-TW", () => {
  renderAt("/");
  expect(screen.getByRole("heading", { name: "ESG 獎" })).toBeInTheDocument();
});

it("switches language to English", async () => {
  renderAt("/");
  await userEvent.click(screen.getByRole("combobox", { name: "語言" }));
  await userEvent.click(await screen.findByRole("option", { name: "English" }));
  expect(await screen.findByRole("heading", { name: "ESG Award" })).toBeInTheDocument();
});

it("shows not-found for unknown routes", () => {
  renderAt("/nope");
  expect(screen.getByText("找不到頁面。")).toBeInTheDocument();
});
