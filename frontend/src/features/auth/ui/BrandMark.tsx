import { EcoIcon } from "./EcoIcon";
import { Box } from "@mui/material";
import { tokens } from "../../../app/tokens";

/** Round green badge with the leaf icon (pen: Logo 64px, Avatar 56px). */
export function BrandMark({ size = tokens.size.avatar }: { size?: number }) {
  return (
    <Box
      aria-hidden
      sx={{
        width: size,
        height: size,
        borderRadius: "50%",
        bgcolor: "primary.main",
        color: "primary.contrastText",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        flexShrink: 0,
      }}
    >
      <EcoIcon sx={{ fontSize: Math.round(size * 0.53) }} />
    </Box>
  );
}
