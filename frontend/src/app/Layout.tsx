import { AppBar, Box, Container, Toolbar, Typography } from "@mui/material";
import { Outlet } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { LanguageSwitcher } from "../shared/LanguageSwitcher";

export function Layout() {
  const { t } = useTranslation();
  return (
    <>
      <AppBar position="static">
        <Toolbar>
          <Typography variant="h6" component="h1" sx={{ flexGrow: 1 }}>
            {t("app.title")}
          </Typography>
          <LanguageSwitcher />
        </Toolbar>
      </AppBar>
      <Container component="main" sx={{ py: 3 }}>
        <Box>
          <Outlet />
        </Box>
      </Container>
    </>
  );
}
