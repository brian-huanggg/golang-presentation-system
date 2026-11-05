import { Alert, Button, MenuItem, Paper, Stack, TextField, Typography } from "@mui/material";
import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, api, unwrap } from "../../api/client";
import {
  DEPARTMENTS,
  toRequest,
  validateRegisterForm,
  type FieldCode,
  type RegisterField,
  type RegisterForm,
} from "./validation";

type FieldErrors = Partial<Record<RegisterField, FieldCode>>;

// Problem codes with their own message (I18N-003: server text is English, the UI translates the code).
const KNOWN_PROBLEMS = ["deadline_passed", "employee_id_taken", "validation_failed", "invalid_request"];

function fieldErrorsFrom(err: unknown): FieldErrors {
  const out: FieldErrors = {};
  if (err instanceof ApiError) {
    for (const e of err.problem?.errors ?? []) out[e.field as RegisterField] = e.code as FieldCode;
  }
  return out;
}

export function RegisterPage() {
  const { t, i18n } = useTranslation();
  const [form, setForm] = useState<RegisterForm>({
    employeeId: "",
    fullName: "",
    department: "",
    password: "",
    // I18N-002: start from the language the visitor is already using.
    preferredLanguage: i18n.language === "en" ? "en" : "zh-TW",
    email: "",
  });
  const [errors, setErrors] = useState<FieldErrors>({});

  const register = useMutation({
    mutationFn: async (f: RegisterForm) =>
      unwrap(await api.POST("/auth/register", { body: toRequest(f) })),
    onError: (err) => setErrors(fieldErrorsFrom(err)),
  });

  const set = (field: RegisterField) => (value: string) => {
    setForm((f) => ({ ...f, [field]: value }));
    setErrors((e) => ({ ...e, [field]: undefined }));
  };

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    const found = validateRegisterForm(form);
    setErrors(found);
    if (Object.keys(found).length === 0) register.mutate(form);
  }

  const helper = (field: RegisterField) =>
    errors[field] ? t(`auth.register.fieldErrors.${errors[field]}`) : undefined;

  if (register.isSuccess) {
    return (
      <Paper sx={{ p: 3, maxWidth: 480, mx: "auto" }}>
        <Alert severity="success">{t("auth.register.success")}</Alert>
      </Paper>
    );
  }

  const problemCode = register.error instanceof ApiError ? register.error.problem?.code : undefined;
  const bannerKey = !register.error
    ? undefined
    : problemCode && KNOWN_PROBLEMS.includes(problemCode)
      ? problemCode
      : "unexpected";
  // Field-level messages already explain a validation_failed response.
  const showBanner = bannerKey && bannerKey !== "validation_failed";

  return (
    <Paper component="form" onSubmit={onSubmit} noValidate sx={{ p: 3, maxWidth: 480, mx: "auto" }}>
      <Stack spacing={2}>
        <Typography variant="h5" component="h2">
          {t("auth.register.title")}
        </Typography>
        {showBanner && <Alert severity="error">{t(`auth.register.problems.${bannerKey}`)}</Alert>}

        <TextField
          label={t("auth.register.employeeId")}
          value={form.employeeId}
          onChange={(e) => set("employeeId")(e.target.value)}
          error={!!errors.employeeId}
          helperText={helper("employeeId") ?? t("auth.register.employeeIdHint")}
          required
          autoComplete="username"
          slotProps={{ htmlInput: { inputMode: "numeric" } }}
        />
        <TextField
          label={t("auth.register.fullName")}
          value={form.fullName}
          onChange={(e) => set("fullName")(e.target.value)}
          error={!!errors.fullName}
          helperText={helper("fullName")}
          required
          autoComplete="name"
        />
        <TextField
          select
          label={t("auth.register.department")}
          value={form.department}
          onChange={(e) => set("department")(e.target.value)}
          error={!!errors.department}
          helperText={helper("department")}
          required
        >
          {DEPARTMENTS.map((d) => (
            <MenuItem key={d} value={d}>
              {d}
            </MenuItem>
          ))}
        </TextField>
        <TextField
          type="password"
          label={t("auth.register.password")}
          value={form.password}
          onChange={(e) => set("password")(e.target.value)}
          error={!!errors.password}
          helperText={helper("password")}
          required
          autoComplete="new-password"
        />
        <TextField
          select
          label={t("auth.register.preferredLanguage")}
          value={form.preferredLanguage}
          onChange={(e) => set("preferredLanguage")(e.target.value)}
          error={!!errors.preferredLanguage}
          helperText={helper("preferredLanguage")}
          required
        >
          <MenuItem value="zh-TW">{t("language.zh-TW")}</MenuItem>
          <MenuItem value="en">{t("language.en")}</MenuItem>
        </TextField>
        <TextField
          type="email"
          label={t("auth.register.email")}
          value={form.email}
          onChange={(e) => set("email")(e.target.value)}
          error={!!errors.email}
          helperText={helper("email") ?? t("auth.register.emailHint")}
          autoComplete="email"
        />

        <Button type="submit" variant="contained" disabled={register.isPending}>
          {t("auth.register.submit")}
        </Button>
      </Stack>
    </Paper>
  );
}
