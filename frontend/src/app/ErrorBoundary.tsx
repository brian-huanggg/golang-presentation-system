import { Component, type ReactNode } from "react";
import { Alert, Button } from "@mui/material";
import i18n from "../i18n";

interface State {
  failed: boolean;
}

export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <Alert
        severity="error"
        action={<Button onClick={() => location.reload()}>{i18n.t("error.reload")}</Button>}
      >
        {i18n.t("error.unexpected")}
      </Alert>
    );
  }
}
