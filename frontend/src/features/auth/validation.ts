import type { components } from "../../api/generated/schema";

export type RegisterRequest = components["schemas"]["RegisterRequest"];
export type RegisterField = keyof RegisterRequest;

export const DEPARTMENTS = ["TSID", "BCID", "DEID", "NTAD"] as const;

/** Field error codes the backend can return; the server stays authoritative. */
export type FieldCode = "required" | "invalid_format" | "invalid_value" | "too_long";

export type RegisterForm = {
  employeeId: string;
  fullName: string;
  department: string;
  password: string;
  preferredLanguage: string;
  email: string;
};

// REG-002: exactly six ASCII digits; spaces and symbols are rejected, not trimmed.
const EMPLOYEE_ID = /^[0-9]{6}$/;
// Deliberately loose (REG-008: format-checked only). The server does the real parse.
const EMAIL = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Mirrors the backend rules so most mistakes are caught before a round trip. */
export function validateRegisterForm(f: RegisterForm): Partial<Record<RegisterField, FieldCode>> {
  const errors: Partial<Record<RegisterField, FieldCode>> = {};
  if (!f.employeeId) errors.employeeId = "required";
  else if (!EMPLOYEE_ID.test(f.employeeId)) errors.employeeId = "invalid_format";

  const name = f.fullName.trim();
  if (!name) errors.fullName = "required";
  else if ([...name].length > 100) errors.fullName = "too_long";

  if (!f.department) errors.department = "required";
  if (!f.preferredLanguage) errors.preferredLanguage = "required";

  if (!f.password) errors.password = "required";
  else if (f.password.length > 128) errors.password = "too_long";

  const email = f.email.trim();
  if (email && !EMAIL.test(email)) errors.email = "invalid_format";
  return errors;
}

/** Builds the request body; an empty optional email is omitted rather than sent as "". */
export function toRequest(f: RegisterForm): RegisterRequest {
  const email = f.email.trim();
  return {
    employeeId: f.employeeId,
    fullName: f.fullName.trim(),
    department: f.department as RegisterRequest["department"],
    password: f.password,
    preferredLanguage: f.preferredLanguage as RegisterRequest["preferredLanguage"],
    ...(email ? { email } : {}),
  };
}

export type ChangePasswordForm = { currentPassword: string; newPassword: string; confirmPassword: string };
export type ChangePasswordField = keyof ChangePasswordForm;
export type ChangePasswordCode = FieldCode | "mismatch" | "password_incorrect" | `password_${string}`;

/** REG-007: only presence, size and confirmation here; the REG-005 policy stays on the server. */
export function validateChangePasswordForm(f: ChangePasswordForm): Partial<Record<ChangePasswordField, ChangePasswordCode>> {
  const errors: Partial<Record<ChangePasswordField, ChangePasswordCode>> = {};
  if (!f.currentPassword) errors.currentPassword = "required";
  if (!f.newPassword) errors.newPassword = "required";
  else if (f.newPassword.length > 128) errors.newPassword = "too_long";
  if (!errors.newPassword && f.confirmPassword !== f.newPassword) errors.confirmPassword = "mismatch";
  return errors;
}
