import { InputAdornment, TextField, type TextFieldProps } from "@mui/material";
import type { ReactNode } from "react";

export type AuthTextFieldProps = Omit<TextFieldProps, "variant" | "slotProps"> & {
  /** Leading icon (pen: Start Icon, 22px). Turns primary while the field has focus. */
  icon?: ReactNode;
  /** Trailing control, e.g. the password visibility toggle (pen: End Icon). */
  endAdornment?: ReactNode;
  inputMode?: "numeric" | "text" | "email";
};

/** Outlined field with floating label, matching the pen's 56px / 4px-radius input. */
export function AuthTextField({ icon, endAdornment, inputMode, sx, ...rest }: AuthTextFieldProps) {
  return (
    <TextField
      variant="outlined"
      fullWidth
      sx={[
        { "& .MuiInputAdornment-root": { color: "text.secondary" }, "& .Mui-focused .MuiInputAdornment-root": { color: "primary.main" } },
        ...(Array.isArray(sx) ? sx : [sx]),
      ]}
      slotProps={{
        input: {
          startAdornment: icon ? <InputAdornment position="start">{icon}</InputAdornment> : undefined,
          endAdornment: endAdornment ? <InputAdornment position="end">{endAdornment}</InputAdornment> : undefined,
        },
        htmlInput: inputMode ? { inputMode } : undefined,
      }}
      {...rest}
    />
  );
}
