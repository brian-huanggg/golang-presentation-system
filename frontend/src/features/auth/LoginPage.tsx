import BadgeOutlined from "@mui/icons-material/BadgeOutlined";
import { Alert, Stack } from "@mui/material";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { ApiError, api, unwrap } from "../../api/client";
import { setLanguage } from "../../i18n";
import heroImage from "./assets/hero-login.jpg";
import {
  AuthFooterLink,
  AuthHeader,
  AuthSplitLayout,
  AuthSubmitButton,
  AuthTextField,
  PasswordField,
} from "./ui";
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
    <AuthSplitLayout heroImage={heroImage} heroTitle={t("auth.login.heroTitle")} heroSubtitle={t("auth.login.heroSubtitle")}>
      <Stack component="form" onSubmit={onSubmit} noValidate spacing={3}>
        <AuthHeader title={t("auth.login.title")} subtitle={t("auth.login.subtitle")} />
        {banner && <Alert severity="error">{t(`auth.login.problems.${banner}`)}</Alert>}
        <Stack spacing={2.5}>
          <AuthTextField
            label={t("auth.login.employeeId")}
            placeholder={t("auth.employeeIdPlaceholder")}
            icon={<BadgeOutlined fontSize="small" />}
            value={employeeId}
            onChange={(e) => {
              setEmployeeId(e.target.value);
              setIdError(false);
            }}
            error={idError}
            helperText={idError ? t("auth.login.employeeIdInvalid") : undefined}
            required
            autoComplete="username"
            inputMode="numeric"
          />
          <PasswordField
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
        </Stack>
        <AuthSubmitButton disabled={login.isPending}>{t("auth.login.submit")}</AuthSubmitButton>
        <AuthFooterLink text={t("auth.login.noAccount")} linkText={t("auth.login.register")} to="/register" />
      </Stack>
    </AuthSplitLayout>
  );
}
