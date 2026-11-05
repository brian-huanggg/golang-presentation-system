import { Button, type ButtonProps } from "@mui/material";
import { tokens } from "../../../app/tokens";

/** Full-width primary action (pen: 48px, 12px radius, uppercase label). */
export function AuthSubmitButton(props: Omit<ButtonProps, "type" | "variant" | "fullWidth">) {
  return (
    <Button
      type="submit"
      variant="contained"
      fullWidth
      {...props}
      sx={{ height: tokens.size.button, boxShadow: tokens.shadow.button }}
    />
  );
}
