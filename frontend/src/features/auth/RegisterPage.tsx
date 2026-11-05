import ApartmentOutlined from "@mui/icons-material/ApartmentOutlined";
import BadgeOutlined from "@mui/icons-material/BadgeOutlined";
import MailOutline from "@mui/icons-material/MailOutlined";
import PersonOutline from "@mui/icons-material/PersonOutlined";
import TranslateOutlined from "@mui/icons-material/LanguageOutlined";
import { Alert, MenuItem, Stack } from "@mui/material";
import { useMutation } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import { ApiError, api, unwrap } from "../../api/client";
import heroImage from "./assets/hero-register.jpg";
import {
  AuthCardLayout,
  AuthFooterLink,
  AuthHeader,
  AuthSubmitButton,
  AuthTextField,
  FieldHint,
  PasswordField,
} from "./ui";
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
      <AuthCardLayout backdrop={heroImage}>
        <Stack spacing={3}>
          <AuthHeader title={t("auth.register.title")} subtitle={t("auth.register.subtitle")} />
          <Alert severity="success">{t("auth.register.success")}</Alert>
          <AuthFooterLink text={t("auth.register.haveAccount")} linkText={t("auth.register.toLogin")} to="/login" />
        </Stack>
      </AuthCardLayout>
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
    <AuthCardLayout backdrop={heroImage}>
      <Stack component="form" onSubmit={onSubmit} noValidate spacing={3}>
        <AuthHeader title={t("auth.register.title")} subtitle={t("auth.register.subtitle")} />
        {showBanner && <Alert severity="error">{t(`auth.register.problems.${bannerKey}`)}</Alert>}

        <Stack spacing={2.5}>
          <AuthTextField
            label={t("auth.register.fullName")}
            icon={<PersonOutline fontSize="small" />}
            value={form.fullName}
            onChange={(e) => set("fullName")(e.target.value)}
            error={!!errors.fullName}
            helperText={helper("fullName")}
            required
            autoComplete="name"
          />
          <AuthTextField
            label={t("auth.register.employeeId")}
            placeholder={t("auth.employeeIdPlaceholder")}
            icon={<BadgeOutlined fontSize="small" />}
            value={form.employeeId}
            onChange={(e) => set("employeeId")(e.target.value)}
            error={!!errors.employeeId}
            helperText={helper("employeeId") ?? t("auth.register.employeeIdHint")}
            required
            autoComplete="username"
            inputMode="numeric"
          />
          <AuthTextField
            select
            label={t("auth.register.department")}
            icon={<ApartmentOutlined fontSize="small" />}
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
          </AuthTextField>
          <PasswordField
            label={t("auth.register.password")}
            value={form.password}
            onChange={(e) => set("password")(e.target.value)}
            error={!!errors.password}
            helperText={helper("password") ?? <FieldHint>{t("auth.register.passwordHint")}</FieldHint>}
            required
            autoComplete="new-password"
          />
          <AuthTextField
            select
            label={t("auth.register.preferredLanguage")}
            icon={<TranslateOutlined fontSize="small" />}
            value={form.preferredLanguage}
            onChange={(e) => set("preferredLanguage")(e.target.value)}
            error={!!errors.preferredLanguage}
            helperText={helper("preferredLanguage")}
            required
          >
            <MenuItem value="zh-TW">{t("language.zh-TW")}</MenuItem>
            <MenuItem value="en">{t("language.en")}</MenuItem>
          </AuthTextField>
          <AuthTextField
            type="email"
            label={t("auth.register.email")}
            icon={<MailOutline fontSize="small" />}
            value={form.email}
            onChange={(e) => set("email")(e.target.value)}
            error={!!errors.email}
            helperText={helper("email") ?? t("auth.register.emailHint")}
            autoComplete="email"
            inputMode="email"
          />
        </Stack>

        <AuthSubmitButton disabled={register.isPending}>{t("auth.register.submit")}</AuthSubmitButton>
        <AuthFooterLink text={t("auth.register.haveAccount")} linkText={t("auth.register.toLogin")} to="/login" />
      </Stack>
    </AuthCardLayout>
  );
}
