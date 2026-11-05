import { Link, Typography } from "@mui/material";
import { Link as RouterLink, type RouteObject } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { LoginPage } from "../features/auth/LoginPage";
import { useSession } from "../features/auth/useSession";
import { RegisterPage } from "../features/auth/RegisterPage";
import { Layout } from "./Layout";

function Home() {
  const { t } = useTranslation();
  const { data: user } = useSession();
  return (
    <>
      <Typography>{t("app.home")}</Typography>
      {!user && (
        <>
          <Link component={RouterLink} to="/login" sx={{ mr: 2 }}>
            {t("auth.login.title")}
          </Link>
          <Link component={RouterLink} to="/register">
            {t("auth.register.title")}
          </Link>
        </>
      )}
    </>
  );
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
      { path: "login", element: <LoginPage /> },
      { path: "register", element: <RegisterPage /> },
      { path: "*", element: <NotFound /> },
    ],
  },
];
