import { Link, Stack, Typography } from "@mui/material";
import { Link as RouterLink } from "react-router-dom";

/** "Don't have an account? Register" line under the form. */
export function AuthFooterLink({ text, linkText, to }: { text: string; linkText: string; to: string }) {
  return (
    <Stack direction="row" spacing={0.5} sx={{ justifyContent: "center" }}>
      <Typography variant="body2" sx={{ color: "text.secondary" }}>
        {text}
      </Typography>
      <Link component={RouterLink} to={to} variant="body2" underline="hover">
        {linkText}
      </Link>
    </Stack>
  );
}
