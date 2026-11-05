import InfoOutlined from "@mui/icons-material/InfoOutlined";
import type { ReactNode } from "react";

/** Helper text with a leading info icon (pen: Password Hint). Inline-only so it is valid inside <p>. */
export function FieldHint({ children }: { children: ReactNode }) {
  return (
    <span style={{ display: "inline-flex", alignItems: "flex-start", gap: 6 }}>
      <InfoOutlined aria-hidden sx={{ fontSize: 16, mt: "1px" }} />
      <span>{children}</span>
    </span>
  );
}
