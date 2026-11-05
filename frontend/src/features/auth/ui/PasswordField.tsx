import LockOutlined from "@mui/icons-material/LockOutlined";
import Visibility from "@mui/icons-material/Visibility";
import VisibilityOff from "@mui/icons-material/VisibilityOff";
import { IconButton } from "@mui/material";
import { useState } from "react";
import { useTranslation } from "react-i18next";
import { AuthTextField, type AuthTextFieldProps } from "./AuthTextField";

/** Password input with a lock icon and a show/hide toggle. */
export function PasswordField(props: Omit<AuthTextFieldProps, "type" | "icon" | "endAdornment" | "select">) {
  const { t } = useTranslation();
  const [visible, setVisible] = useState(false);
  return (
    <AuthTextField
      {...props}
      type={visible ? "text" : "password"}
      icon={<LockOutlined fontSize="small" />}
      endAdornment={
        <IconButton
          edge="end"
          size="small"
          onClick={() => setVisible((v) => !v)}
          onMouseDown={(e) => e.preventDefault()}
          aria-label={t("auth.togglePassword")}
          aria-pressed={visible}
        >
          {visible ? <VisibilityOff fontSize="small" /> : <Visibility fontSize="small" />}
        </IconButton>
      }
    />
  );
}
