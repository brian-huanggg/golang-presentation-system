import { AppBar, Box, Button, Container, Toolbar, Typography } from "@mui/material";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Link as RouterLink, Outlet, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { api, unwrapEmpty } from "../api/client";
import { SESSION_KEY, useSession } from "../features/auth/useSession";
import { LanguageSwitcher } from "../shared/LanguageSwitcher";

export function Layout() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { data: user } = useSession();

  // AUTH-006: the server deletes the session first; only then does the SPA forget the user.
  const logout = useMutation({
    mutationFn: async () => unwrapEmpty(await api.POST("/auth/logout")),
    onSuccess: () => {
      queryClient.setQueryData(SESSION_KEY, null);
      navigate("/login", { replace: true });
    },
  });

  return (
    <>
      <AppBar position="static">
        <Toolbar>
          <Typography variant="h6" component="h1" sx={{ flexGrow: 1 }}>
            {t("app.title")}
          </Typography>
          {user && (
            <>
              <Typography sx={{ mr: 2 }}>{t("auth.logout.signedInAs", { name: user.fullName })}</Typography>
              <Button color="inherit" component={RouterLink} to="/account/password">
                {t("auth.changePassword.link")}
              </Button>
              <Button color="inherit" onClick={() => logout.mutate()} disabled={logout.isPending}>
                {t("auth.logout.button")}
              </Button>
            </>
          )}
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
