import { Alert, Stack } from "@mui/material";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { Navigate } from "react-router-dom";
import { ApiError, api, unwrapEmpty } from "../../api/client";
import { AuthHeader, AuthSubmitButton, FieldHint, PasswordField } from "./ui";
import { SESSION_KEY, useSession } from "./useSession";
import {
  validateChangePasswordForm,
  type ChangePasswordCode,
  type ChangePasswordField,
  type ChangePasswordForm,
} from "./validation";

type FieldErrors = Partial<Record<ChangePasswordField, ChangePasswordCode>>;

const EMPTY: ChangePasswordForm = { currentPassword: "", newPassword: "", confirmPassword: "" };

// A password policy violation (REG-005) arrives once per rule; show the first per field.
function fieldErrorsFrom(err: unknown): FieldErrors {
  const out: FieldErrors = {};
  if (err instanceof ApiError) {
    for (const e of err.problem?.errors ?? []) {
      const field = e.field as ChangePasswordField;
      if (!out[field]) out[field] = e.code as ChangePasswordCode;
    }
  }
  return out;
}

/** REG-007 / AUTH-007: change the password; other sessions are ended by the server. */
export function ChangePasswordPage() {
  const { t } = useTranslation();
  const queryClient = useQueryClient();
  const { data: user, isLoading } = useSession();
  const [form, setForm] = useState<ChangePasswordForm>(EMPTY);
  const [errors, setErrors] = useState<FieldErrors>({});

  const change = useMutation({
    mutationFn: async (f: ChangePasswordForm) =>
      unwrapEmpty(await api.POST("/auth/password", { body: { currentPassword: f.currentPassword, newPassword: f.newPassword } })),
    onSuccess: () => {
      setForm(EMPTY);
      // mustChangePassword may have flipped (AUTH-008), so re-read the user.
      void queryClient.invalidateQueries({ queryKey: SESSION_KEY });
    },
    onError: (err) => {
      setErrors(fieldErrorsFrom(err));
      // The password the user typed was rejected; do not keep it around.
      setForm((f) => ({ ...f, currentPassword: "" }));
    },
  });

  if (isLoading) return null;
  if (!user) return <Navigate to="/login" replace />;

  const set = (field: ChangePasswordField) => (value: string) => {
    setForm((f) => ({ ...f, [field]: value }));
    setErrors((e) => ({ ...e, [field]: undefined }));
  };
  const helper = (field: ChangePasswordField) =>
    errors[field] ? t(`auth.register.fieldErrors.${errors[field]}`, { defaultValue: t("auth.register.fieldErrors.invalid_value") }) : undefined;

  function onSubmit(e: FormEvent) {
    e.preventDefault();
    change.reset();
    const found = validateChangePasswordForm(form);
    setErrors(found);
    if (Object.keys(found).length === 0) change.mutate(form);
  }

  const problemCode = change.error instanceof ApiError ? change.error.problem?.code : undefined;
  const hasFieldErrors = Object.values(errors).some(Boolean);
  const banner = !change.error || (problemCode === "validation_failed" && hasFieldErrors)
    ? undefined
    : problemCode === "conflict" || problemCode === "invalid_request"
      ? problemCode
      : "unexpected";

  return (
    <Stack component="form" onSubmit={onSubmit} noValidate spacing={3} sx={{ maxWidth: 480 }}>
      <AuthHeader title={t("auth.changePassword.title")} subtitle={t("auth.changePassword.subtitle")} />
      {change.isSuccess && <Alert severity="success">{t("auth.changePassword.success")}</Alert>}
      {banner && <Alert severity="error">{t(`auth.changePassword.problems.${banner}`)}</Alert>}
      <Stack spacing={2.5}>
        <PasswordField
          label={t("auth.changePassword.current")}
          value={form.currentPassword}
          onChange={(e) => set("currentPassword")(e.target.value)}
          error={!!errors.currentPassword}
          helperText={helper("currentPassword")}
          required
          autoComplete="current-password"
        />
        <PasswordField
          label={t("auth.changePassword.new")}
          value={form.newPassword}
          onChange={(e) => set("newPassword")(e.target.value)}
          error={!!errors.newPassword}
          helperText={helper("newPassword") ?? <FieldHint>{t("auth.register.passwordHint")}</FieldHint>}
          required
          autoComplete="new-password"
        />
        <PasswordField
          label={t("auth.changePassword.confirm")}
          value={form.confirmPassword}
          onChange={(e) => set("confirmPassword")(e.target.value)}
          error={!!errors.confirmPassword}
          helperText={helper("confirmPassword")}
          required
          autoComplete="new-password"
        />
      </Stack>
      <AuthSubmitButton disabled={change.isPending}>{t("auth.changePassword.submit")}</AuthSubmitButton>
    </Stack>
  );
}
