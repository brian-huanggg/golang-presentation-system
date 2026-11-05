import { Box, Typography } from "@mui/material";
import type { ReactNode } from "react";
import { tokens } from "../../../app/tokens";
import { LanguageSwitcher } from "../../../shared/LanguageSwitcher";
import { BrandMark } from "./BrandMark";

type Props = { heroImage: string; heroTitle: string; heroSubtitle: string; children: ReactNode };

/**
 * Login layout (pen: "Login - Employee"): 500px hero panel on the left, form centred on the right.
 * The hero panel is hidden below the md breakpoint so the form owns the narrow screen (NFR-UX-002).
 */
export function AuthSplitLayout({ heroImage, heroTitle, heroSubtitle, children }: Props) {
  const { color, size, font, scrim } = tokens;
  return (
    <Box component="main" sx={{ display: "flex", minHeight: "100vh", bgcolor: color.surface }}>
      <Box
        sx={{
          display: { xs: "none", md: "flex" },
          position: "relative",
          overflow: "hidden",
          width: size.heroPanel,
          flexShrink: 0,
          flexDirection: "column",
          alignItems: "center",
          justifyContent: "center",
          gap: 3,
          p: 6,
          background: `linear-gradient(to bottom, rgba(255,255,255,0), ${color.heroTint}), ${color.heroBase}`,
        }}
      >
        <Box aria-hidden sx={{ position: "absolute", inset: 0, backgroundImage: `url(${heroImage})`, backgroundSize: "cover", backgroundPosition: "center" }} />
        <Box aria-hidden sx={{ position: "absolute", inset: 0, backgroundImage: scrim.loginHero }} />
        <Box sx={{ position: "relative", display: "flex", flexDirection: "column", alignItems: "center", gap: 3 }}>
          <BrandMark size={size.logo} />
          <Typography
            component="p"
            sx={{ color: "primary.dark", fontSize: font.sizeHeroTitle, fontWeight: font.weightMedium, lineHeight: 1.25, textAlign: "center", whiteSpace: "pre-line" }}
          >
            {heroTitle}
          </Typography>
          <Typography sx={{ color: color.heroSubtitle, fontSize: font.sizeBody, lineHeight: 1.5, textAlign: "center", maxWidth: 320 }}>
            {heroSubtitle}
          </Typography>
        </Box>
      </Box>
      <Box sx={{ position: "relative", flex: 1, display: "flex", alignItems: "center", justifyContent: "center", px: 2, py: 6 }}>
        <Box sx={{ position: "absolute", top: 16, right: 16 }}>
          <LanguageSwitcher />
        </Box>
        <Box sx={{ width: size.loginForm, maxWidth: "100%" }}>{children}</Box>
      </Box>
    </Box>
  );
}
