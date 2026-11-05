import { useMemo } from "react";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { Providers } from "./providers";
import { createQueryClient } from "./queryClient";
import { routes } from "./routes";

const router = createBrowserRouter(routes);

export function App() {
  const client = useMemo(createQueryClient, []);
  return (
    <Providers client={client}>
      <RouterProvider router={router} />
    </Providers>
  );
}
