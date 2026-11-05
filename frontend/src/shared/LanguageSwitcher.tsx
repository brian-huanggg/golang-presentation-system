import { MenuItem, Select } from "@mui/material";
import { useTranslation } from "react-i18next";
import { LANGUAGES, setLanguage, type Language } from "../i18n";

export function LanguageSwitcher() {
  const { t, i18n } = useTranslation();
  return (
    <Select
      size="small"
      value={i18n.language}
      onChange={(e) => setLanguage(e.target.value as Language)}
      inputProps={{ "aria-label": t("language.label") }}
      sx={{ color: "inherit" }}
    >
      {LANGUAGES.map((l) => (
        <MenuItem key={l} value={l}>
          {t(`language.${l}`)}
        </MenuItem>
      ))}
    </Select>
  );
}
