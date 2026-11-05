import { Box } from "@mui/material";
import type { ReactNode } from "react";
import { tokens } from "../../../app/tokens";
import { LanguageSwitcher } from "../../../shared/LanguageSwitcher";

/** Register layout (pen: "Sign Up - Employee"): full-bleed artwork with a floating white card. */
export function AuthCardLayout({ backdrop, children }: { backdrop: string; children: ReactNode }) {
  const { color, size, radius, shadow, scrim } = tokens;
  return (
    <Box
      component="main"
      sx={{
        position: "relative",
        minHeight: "100vh",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        px: 2,
        py: 6,
        background: `linear-gradient(to bottom, rgba(255,255,255,0), ${color.heroTint}), ${color.heroBase}`,
      }}
    >
      <Box aria-hidden sx={{ position: "absolute", inset: 0, backgroundImage: `url(${backdrop})`, backgroundSize: "cover", backgroundPosition: "center" }} />
      <Box aria-hidden sx={{ position: "absolute", inset: 0, backgroundImage: scrim.registerBackdrop }} />
      <Box sx={{ position: "absolute", top: 16, right: 16 }}>
        <LanguageSwitcher />
      </Box>
      <Box
        sx={{
          position: "relative",
          width: size.registerCard,
          maxWidth: "100%",
          bgcolor: color.surface,
          borderRadius: `${radius.card}px`,
          boxShadow: shadow.card,
          px: { xs: 2.5, sm: 6 },
          py: { xs: 3, sm: 5 },
        }}
      >
        {children}
      </Box>
    </Box>
  );
}
