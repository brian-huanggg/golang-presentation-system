import { MenuItem, Select } from "@mui/material";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useTranslation } from "react-i18next";
import { api, unwrap } from "../api/client";
import { SESSION_KEY, useSession } from "../features/auth/useSession";
import { LANGUAGES, setLanguage, type Language } from "../i18n";

export function LanguageSwitcher() {
  const { t, i18n } = useTranslation();
  const queryClient = useQueryClient();
  const { data: user } = useSession();

  // I18N-002: a logged-in user's choice is saved to the account. A failed save is
  // not rolled back: the UI language still changes and the next login restores the old one.
  const save = useMutation({
    mutationFn: async (preferredLanguage: Language) =>
      unwrap(await api.PATCH("/auth/me", { body: { preferredLanguage } })),
    onSuccess: (updated) => queryClient.setQueryData(SESSION_KEY, updated),
  });

  function onChange(lang: Language) {
    setLanguage(lang);
    if (user) save.mutate(lang);
  }

  return (
    <Select
      size="small"
      value={i18n.language}
      onChange={(e) => onChange(e.target.value as Language)}
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
