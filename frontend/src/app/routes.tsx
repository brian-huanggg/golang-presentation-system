import { Typography } from "@mui/material";
import type { RouteObject } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { Layout } from "./Layout";

function Home() {
  const { t } = useTranslation();
  return <Typography>{t("app.home")}</Typography>;
}

function NotFound() {
  const { t } = useTranslation();
  return <Typography>{t("notFound")}</Typography>;
}

// Feature routes (auth, team, idea, admin) are added here as each feature lands.
export const routes: RouteObject[] = [
  {
    element: <Layout />,
    children: [
      { index: true, element: <Home /> },
      { path: "*", element: <NotFound /> },
    ],
  },
];
