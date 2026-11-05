import { Alert, Button, Link, Paper, Stack, TextField, Typography } from "@mui/material";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Link as RouterLink, useNavigate } from "react-router-dom";
import { ApiError, api, unwrap } from "../../api/client";
import { setLanguage } from "../../i18n";
import { SESSION_KEY, type SessionUser } from "./useSession";

// AUTH-001: exactly six digits. An email address is rejected here before any request.
const EMPLOYEE_ID = /^[0-9]{6}$/;

export function LoginPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [employeeId, setEmployeeId] = useState("");
  const [password, setPassword] = useState("");
  const [idError, setIdError] = useState(false);
  const [pwError, setPwError] = useState(false);

  const login = useMutation({
    mutationFn: async () => unwrap(await api.POST("/auth/login", { body: { employeeId, password } })),
    onSuccess: (user: SessionUser) => {
      queryClient.setQueryData(SESSION_KEY, user);
      // I18N-002: the saved preference wins on every login.
      void setLanguage(user.preferredLanguage);
      navigate("/", { replace: true });
    },
  });

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const badId = !EMPLOYEE_ID.test(employeeId);
    const badPw = password === "";
    setIdError(badId);
    setPwError(badPw);
    if (!badId && !badPw) login.mutate();
  }

  // AUTH-003: the server answers every credential failure alike, so the UI does too.
  const code = login.error instanceof ApiError ? login.error.problem?.code : undefined;
  const banner = !login.error ? undefined : code === "invalid_credentials" ? "invalid_credentials" : "unexpected";

  return (
    <Paper component="form" onSubmit={onSubmit} noValidate sx={{ p: 3, maxWidth: 480, mx: "auto" }}>
      <Stack spacing={2}>
        <Typography variant="h5" component="h2">
          {t("auth.login.title")}
        </Typography>
        {banner && <Alert severity="error">{t(`auth.login.problems.${banner}`)}</Alert>}
        <TextField
          label={t("auth.login.employeeId")}
          value={employeeId}
          onChange={(e) => {
            setEmployeeId(e.target.value);
            setIdError(false);
          }}
          error={idError}
          helperText={idError ? t("auth.login.employeeIdInvalid") : t("auth.register.employeeIdHint")}
          required
          autoComplete="username"
          slotProps={{ htmlInput: { inputMode: "numeric" } }}
        />
        <TextField
          type="password"
          label={t("auth.login.password")}
          value={password}
          onChange={(e) => {
            setPassword(e.target.value);
            setPwError(false);
          }}
          error={pwError}
          helperText={pwError ? t("auth.register.fieldErrors.required") : undefined}
          required
          autoComplete="current-password"
        />
        <Button type="submit" variant="contained" disabled={login.isPending}>
          {t("auth.login.submit")}
        </Button>
        <Link component={RouterLink} to="/register">
          {t("auth.login.toRegister")}
        </Link>
      </Stack>
    </Paper>
  );
}
