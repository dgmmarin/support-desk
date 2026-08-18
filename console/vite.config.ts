import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  server: { proxy: { "/queue": "http://localhost:8080", "/analytics": "http://localhost:8080" } },
  test: { environment: "jsdom", globals: true, setupFiles: ["./vitest.setup.ts"] },
} as any);
