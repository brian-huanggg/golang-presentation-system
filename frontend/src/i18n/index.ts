import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import en from "./locales/en.json";
import zhTW from "./locales/zh-TW.json";

export const LANGUAGES = ["zh-TW", "en"] as const;
export type Language = (typeof LANGUAGES)[number];

const STORAGE_KEY = "lang";

// I18N-002: before login the browser's last choice is used. After login the
// profile value (saved via API) takes over; that wiring belongs to the auth feature.
export function initialLanguage(): Language {
  try {
    const saved = localStorage.getItem(STORAGE_KEY);
    if (LANGUAGES.includes(saved as Language)) return saved as Language;
  } catch {
    // Storage can be blocked; fall through to the default.
  }
  return "zh-TW";
}

export function setLanguage(lang: Language): void {
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // Not persisting is acceptable; the in-memory switch still applies.
  }
  void i18n.changeLanguage(lang);
}

void i18n.use(initReactI18next).init({
  resources: { "zh-TW": { translation: zhTW }, en: { translation: en } },
  lng: initialLanguage(),
  fallbackLng: "en",
  interpolation: { escapeValue: false }, // React already escapes.
});

export default i18n;
