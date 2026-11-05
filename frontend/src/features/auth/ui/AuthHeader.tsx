import { Stack, Typography } from "@mui/material";
import { tokens } from "../../../app/tokens";
import { BrandMark } from "./BrandMark";

/** Avatar + page title (h1) + subtitle, centred. */
export function AuthHeader({ title, subtitle }: { title: string; subtitle: string }) {
  return (
    <Stack spacing={1} sx={{ alignItems: "center", textAlign: "center" }}>
      <BrandMark />
      <Typography
        variant="h1"
        sx={{ mt: 1, fontSize: tokens.font.sizeTitle, fontWeight: tokens.font.weightMedium, color: "text.primary" }}
      >
        {title}
      </Typography>
      <Typography sx={{ color: "text.secondary", fontSize: tokens.font.sizeBody }}>{subtitle}</Typography>
    </Stack>
  );
}
