// Design tokens. Source of truth: design/login-page-template.pen (variables + frame values).
// Components read them through the MUI theme (theme.ts) or, for layout values with no
// theme slot, import `tokens` directly. Do not hard-code these values elsewhere.
export const tokens = {
  color: {
    primaryMain: "#2E7D32",
    primaryDark: "#1B5E20",
    primaryLight: "#4CAF50",
    textPrimary: "#1F2A22DE",
    textSecondary: "#00000099",
    placeholder: "#00000066",
    divider: "#0000001F",
    outline: "#0000003B",
    surface: "#FFFFFF",
    onPrimary: "#FFFFFF",
    // Hero panel
    heroBase: "#F6F8F1",
    heroTint: "#E3EED9",
    heroSubtitle: "#2E4A33",
  },
  font: {
    family: '"Roboto", "Noto Sans TC", system-ui, -apple-system, "Segoe UI", sans-serif',
    sizeTitle: 30,
    sizeHeroTitle: 38,
    sizeBody: 16,
    sizeSmall: 14,
    sizeLabel: 12,
    sizeButton: 15,
    weightMedium: 500,
    buttonLetterSpacing: 0.46,
  },
  radius: { field: 4, button: 12, card: 16 },
  size: {
    field: 56,
    button: 48,
    logo: 64,
    avatar: 56,
    heroPanel: 500,
    loginForm: 440,
    registerCard: 536,
  },
  shadow: {
    card: "0 16px 48px rgba(27, 94, 32, 0.15)", // #1B5E20 @ 15%
    button: "0 3px 1px rgba(0, 0, 0, 0.2), 0 2px 2px rgba(0, 0, 0, 0.14)",
  },
  // Scrims laid over the hero artwork so text stays legible.
  scrim: {
    loginHero: "radial-gradient(ellipse 140% 70% at 50% 50%, rgba(255,255,255,0.7), rgba(255,255,255,0))",
    registerBackdrop: "radial-gradient(ellipse 90% 120% at 50% 50%, rgba(255,255,255,0.5), rgba(255,255,255,0))",
  },
} as const;
