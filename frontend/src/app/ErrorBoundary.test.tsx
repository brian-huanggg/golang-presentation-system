import { render, screen } from "@testing-library/react";
import "../i18n";
import i18n from "../i18n";
import { ErrorBoundary } from "./ErrorBoundary";

function Boom(): never {
  throw new Error("boom");
}

it("shows a fallback when a child throws", async () => {
  await i18n.changeLanguage("en");
  vi.spyOn(console, "error").mockImplementation(() => {});
  render(
    <ErrorBoundary>
      <Boom />
    </ErrorBoundary>,
  );
  expect(screen.getByText("Something went wrong.")).toBeInTheDocument();
});
