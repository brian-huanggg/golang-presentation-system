import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  server: {
    // Same-origin in dev so the session and CSRF cookies behave as in production.
    proxy: { "/api": "http://localhost:8080" },
  },
  test: {
    environment: "jsdom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
    // userEvent typing through MUI fields is slow on the 2-vCPU dev VM; 5s default flakes under parallel load.
    testTimeout: 20000,
    coverage: {
      provider: "v8",
      // Architecture 6: entry point and generated code are not measured.
      exclude: ["src/main.tsx", "src/api/generated/**", "src/test-setup.ts"],
    },
  },
});
