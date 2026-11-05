import { createTheme } from "@mui/material/styles";
import { tokens } from "./tokens";

const { color, font, radius } = tokens;

export const theme = createTheme({
  palette: {
    primary: { main: color.primaryMain, dark: color.primaryDark, light: color.primaryLight, contrastText: color.onPrimary },
    text: { primary: color.textPrimary, secondary: color.textSecondary },
    divider: color.divider,
  },
  shape: { borderRadius: radius.field },
  typography: {
    fontFamily: font.family,
    button: { fontSize: font.sizeButton, fontWeight: font.weightMedium, letterSpacing: font.buttonLetterSpacing },
  },
  components: {
    MuiOutlinedInput: {
      styleOverrides: {
        root: {
          "& .MuiOutlinedInput-notchedOutline": { borderColor: color.outline },
          "& input::placeholder": { color: color.placeholder, opacity: 1 },
        },
      },
    },
    MuiButton: {
      styleOverrides: {
        contained: { borderRadius: radius.button },
      },
    },
    MuiLink: { styleOverrides: { root: { fontWeight: font.weightMedium } } },
  },
});
