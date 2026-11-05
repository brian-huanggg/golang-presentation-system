import {
  Alert,
  Box,
  Button,
  MenuItem,
  Paper,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableContainer,
  TableHead,
  TablePagination,
  TableRow,
  TableSortLabel,
  TextField,
  Typography,
} from "@mui/material";
import { useQuery } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, api, unwrap } from "../../api/client";
import type { components } from "../../api/generated/schema";
import { useSession } from "../auth/useSession";

type Track = components["schemas"]["Track"];
type AwardCategory = components["schemas"]["AwardCategory"];
type Department = components["schemas"]["Department"];

const TRACKS: Track[] = ["smart_innovation", "sustainable_development"];
const AWARDS: AwardCategory[] = ["engineering", "administration", "manufacturing_process", "transportation", "public_safety"];
const DEPARTMENTS: Department[] = ["TSID", "BCID", "DEID", "NTAD"];
const PAGE_SIZES = [10, 20, 50, 100];

// "" means "no filter" for every select; the idea filter maps to the API's hasIdea boolean.
interface Filters {
  q: string;
  track: Track | "";
  awardCategory: AwardCategory | "";
  department: Department | "";
  idea: "" | "yes" | "no";
}

const NO_FILTERS: Filters = { q: "", track: "", awardCategory: "", department: "", idea: "" };

/** ADM-001: Admins list and search every team of the event year. */
export function AdminTeamsPage() {
  const { t } = useTranslation();
  const { data: user, isPending: sessionPending } = useSession();
  const [draftQ, setDraftQ] = useState("");
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const [page, setPage] = useState(1);
  const [size, setSize] = useState(20);
  const [descending, setDescending] = useState(false);

  const isAdmin = user?.role === "ADMIN";
  const sort = descending ? "-name" : "name";

  const teams = useQuery({
    queryKey: ["admin", "teams", filters, page, size, sort],
    enabled: isAdmin,
    queryFn: async () =>
      unwrap(
        await api.GET("/admin/teams", {
          params: {
            query: {
              q: filters.q || undefined,
              track: filters.track || undefined,
              awardCategory: filters.awardCategory || undefined,
              department: filters.department || undefined,
              hasIdea: filters.idea === "" ? undefined : filters.idea === "yes",
              page,
              size,
              sort,
            },
          },
        }),
      ),
  });

  // Any change of filter starts again at page 1, or the page could be past the end.
  function applyFilters(next: Filters) {
    setFilters(next);
    setPage(1);
  }

  function onSearch(e: FormEvent) {
    e.preventDefault();
    applyFilters({ ...filters, q: draftQ.trim() });
  }

  function onReset() {
    setDraftQ("");
    applyFilters(NO_FILTERS);
  }

  if (sessionPending) return null;
  // The server enforces this (403); the page just does not pretend otherwise.
  if (!isAdmin) return <Alert severity="error">{t("admin.teams.forbidden")}</Alert>;

  const code = teams.error instanceof ApiError ? teams.error.problem?.code : undefined;
  const problem = !teams.error ? undefined : code === "validation_failed" ? "validation_failed" : "unexpected";

  const select = (
    key: "track" | "awardCategory" | "department" | "idea",
    label: string,
    options: { value: string; label: string }[],
  ) => (
    <TextField
      select
      size="small"
      label={label}
      value={filters[key]}
      onChange={(e) => applyFilters({ ...filters, [key]: e.target.value })}
      sx={{ minWidth: 160 }}
    >
      <MenuItem value="">{t("admin.teams.any")}</MenuItem>
      {options.map((o) => (
        <MenuItem key={o.value} value={o.value}>
          {o.label}
        </MenuItem>
      ))}
    </TextField>
  );

  return (
    <Stack spacing={2}>
      <Typography variant="h5" component="h2">
        {t("admin.teams.title")}
      </Typography>

      <Stack component="form" onSubmit={onSearch} direction="row" spacing={2} useFlexGap sx={{ flexWrap: "wrap" }}>
        <TextField
          size="small"
          label={t("admin.teams.search")}
          value={draftQ}
          onChange={(e) => setDraftQ(e.target.value)}
          slotProps={{ htmlInput: { maxLength: 50 } }}
        />
        {select("track", t("admin.teams.track"), TRACKS.map((v) => ({ value: v, label: t(`catalog.track.${v}`) })))}
        {select("awardCategory", t("admin.teams.award"), AWARDS.map((v) => ({ value: v, label: t(`catalog.award.${v}`) })))}
        {/* I18N-005: department codes are shown as-is. */}
        {select("department", t("admin.teams.department"), DEPARTMENTS.map((v) => ({ value: v, label: v })))}
        {select("idea", t("admin.teams.idea"), [
          { value: "yes", label: t("admin.teams.ideaYes") },
          { value: "no", label: t("admin.teams.ideaNo") },
        ])}
        <Button type="submit" variant="contained">
          {t("admin.teams.searchButton")}
        </Button>
        <Button type="button" onClick={onReset}>
          {t("admin.teams.reset")}
        </Button>
      </Stack>

      {problem && <Alert severity="error">{t(`admin.teams.problems.${problem}`)}</Alert>}

      <Paper variant="outlined">
        <TableContainer>
          <Table size="small">
            <TableHead>
              <TableRow>
                <TableCell sortDirection={descending ? "desc" : "asc"}>
                  <TableSortLabel active direction={descending ? "desc" : "asc"} onClick={() => setDescending((d) => !d)}>
                    {t("admin.teams.colName")}
                  </TableSortLabel>
                </TableCell>
                <TableCell>{t("admin.teams.track")}</TableCell>
                <TableCell>{t("admin.teams.award")}</TableCell>
                <TableCell>{t("admin.teams.department")}</TableCell>
                <TableCell>{t("admin.teams.colLeader")}</TableCell>
                <TableCell align="right">{t("admin.teams.colMembers")}</TableCell>
                <TableCell>{t("admin.teams.colIdea")}</TableCell>
              </TableRow>
            </TableHead>
            <TableBody>
              {teams.data?.items.map((team) => (
                <TableRow key={team.id} hover>
                  <TableCell>{team.name}</TableCell>
                  <TableCell>{t(`catalog.track.${team.track}`)}</TableCell>
                  <TableCell>{t(`catalog.award.${team.awardCategory}`)}</TableCell>
                  <TableCell>{team.department}</TableCell>
                  <TableCell>
                    {team.leader.fullName} ({team.leader.employeeId})
                  </TableCell>
                  <TableCell align="right">{team.memberCount}</TableCell>
                  <TableCell>{team.hasIdea ? t("admin.teams.hasIdea") : t("admin.teams.noIdea")}</TableCell>
                </TableRow>
              ))}
              {teams.data?.items.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7}>
                    <Box sx={{ py: 2, textAlign: "center" }}>{t("admin.teams.empty")}</Box>
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </TableContainer>
        <TablePagination
          component="div"
          count={teams.data?.total ?? 0}
          page={page - 1}
          rowsPerPage={size}
          rowsPerPageOptions={PAGE_SIZES}
          labelRowsPerPage={t("admin.teams.rowsPerPage")}
          labelDisplayedRows={({ from, to, count }) => t("admin.teams.displayedRows", { from, to, count })}
          onPageChange={(_, p) => setPage(p + 1)}
          onRowsPerPageChange={(e) => {
            setSize(Number(e.target.value));
            setPage(1);
          }}
        />
      </Paper>
    </Stack>
  );
}
