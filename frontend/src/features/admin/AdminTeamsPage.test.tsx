import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { api } from "../../api/client";
import i18n from "../../i18n";
import { Providers } from "../../app/providers";
import { createQueryClient } from "../../app/queryClient";
import { routes } from "../../app/routes";

const admin = {
  employeeId: "000001",
  fullName: "管理員",
  role: "ADMIN",
  preferredLanguage: "en",
  mustChangePassword: false,
};
const participant = { ...admin, employeeId: "123456", role: "PARTICIPANT", department: "TSID" };

const team = {
  id: 7,
  eventYear: 2026,
  name: "Green Wave",
  track: "smart_innovation",
  awardCategory: "engineering",
  department: "TSID",
  leader: { employeeId: "100001", fullName: "Lee Leader", role: "LEADER" },
  memberCount: 3,
  hasIdea: true,
};

function reply(status: number, body?: unknown) {
  return {
    ...(status < 300 ? { data: body } : { error: body }),
    response: new Response(null, { status }),
  } as never;
}

type Query = Record<string, unknown>;

function stubApi(opts: { user?: unknown; list?: (q: Query) => ReturnType<typeof reply> } = {}) {
  const queries: Query[] = [];
  vi.spyOn(api, "GET").mockImplementation((async (path: string, init?: { params?: { query?: Query } }) => {
    if (path === "/auth/me") return reply(200, opts.user ?? admin);
    const q = init?.params?.query ?? {};
    queries.push(q);
    return opts.list ? opts.list(q) : reply(200, { items: [team], total: 1, page: 1, size: 20 });
  }) as never);
  return queries;
}

function renderPage() {
  const router = createMemoryRouter(routes, { initialEntries: ["/admin/teams"] });
  render(
    <Providers client={createQueryClient()}>
      <RouterProvider router={router} />
    </Providers>,
  );
}

beforeEach(() => i18n.changeLanguage("en"));
afterEach(() => vi.restoreAllMocks());

describe("AdminTeamsPage", () => {
  it("ADM-001 lists teams with track and award in the UI language", async () => {
    const queries = stubApi();
    renderPage();
    const row = (await screen.findByText("Green Wave")).closest("tr")!;
    expect(within(row).getByText("Smart Innovation")).toBeInTheDocument();
    expect(within(row).getByText("Engineering")).toBeInTheDocument();
    expect(within(row).getByText("TSID")).toBeInTheDocument();
    expect(within(row).getByText("Lee Leader (100001)")).toBeInTheDocument();
    expect(within(row).getByText("Yes")).toBeInTheDocument();
    expect(queries.at(0)).toMatchObject({ page: 1, size: 20, sort: "name" });
    expect(queries.at(0)?.q).toBeUndefined();
  });

  it("ADM-001 searches by name and resets to page 1", async () => {
    const queries = stubApi();
    renderPage();
    await screen.findByText("Green Wave");
    const u = userEvent.setup();
    await u.type(screen.getByLabelText("Team name"), "  green ");
    await u.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ q: "green", page: 1 }));
  });

  it("ADM-001 filters by track, award, department and Idea", async () => {
    const queries = stubApi();
    renderPage();
    await screen.findByText("Green Wave");
    const u = userEvent.setup();
    const pick = async (label: string, option: string) => {
      await u.click(screen.getByRole("combobox", { name: label }));
      await u.click(await screen.findByRole("option", { name: option }));
    };
    await pick("Track", "Sustainable Development");
    await pick("Award category", "Public Safety");
    await pick("Department", "BCID");
    await pick("Idea", "No Idea yet");
    await waitFor(() =>
      expect(queries.at(-1)).toMatchObject({
        track: "sustainable_development",
        awardCategory: "public_safety",
        department: "BCID",
        hasIdea: false,
      }),
    );
  });

  it("ADM-001 reset clears every filter", async () => {
    const queries = stubApi();
    renderPage();
    await screen.findByText("Green Wave");
    const u = userEvent.setup();
    await u.click(screen.getByRole("combobox", { name: "Department" }));
    await u.click(await screen.findByRole("option", { name: "DEID" }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ department: "DEID" }));
    await u.click(screen.getByRole("button", { name: "Reset" }));
    // The unfiltered list is already cached, so no new request is needed to show it.
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Department" })).not.toHaveTextContent("DEID"));
  });

  it("ADM-001 toggles the name sort and pages through results", async () => {
    const queries = stubApi({ list: () => reply(200, { items: [team], total: 45, page: 1, size: 20 }) });
    renderPage();
    await screen.findByText("Green Wave");
    const u = userEvent.setup();
    await u.click(screen.getByRole("button", { name: "Team" }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ sort: "-name" }));
    await u.click(screen.getByRole("button", { name: /next page/i }));
    await waitFor(() => expect(queries.at(-1)).toMatchObject({ page: 2 }));
  });

  it("shows an empty state", async () => {
    stubApi({ list: () => reply(200, { items: [], total: 0, page: 1, size: 20 }) });
    renderPage();
    expect(await screen.findByText("No teams match.")).toBeInTheDocument();
  });

  it("shows an error banner when the request fails", async () => {
    // A 5xx would be retried with back-off; a 4xx fails at once and exercises the same banner.
    stubApi({ list: () => reply(403, { status: 403, code: "forbidden" }) });
    renderPage();
    expect(await screen.findByText("Something went wrong. Please try again later.")).toBeInTheDocument();
  });

  it("R-02 a Participant sees no team list and the request is never made", async () => {
    const queries = stubApi({ user: participant });
    renderPage();
    expect(await screen.findByText("Only Admins can view this page.")).toBeInTheDocument();
    expect(queries).toHaveLength(0);
    expect(screen.queryByRole("link", { name: "All teams" })).not.toBeInTheDocument();
  });

  it("an Admin gets the navigation link", async () => {
    stubApi();
    renderPage();
    expect(await screen.findByRole("link", { name: "All teams" })).toHaveAttribute("href", "/admin/teams");
  });
});
