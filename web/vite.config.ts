import { defineConfig } from "vitest/config";
import react from "@vitejs/plugin-react";
import tailwind from "@tailwindcss/vite";

export default defineConfig({
  base: "./",
  plugins: [react(), tailwind()],
  server: { proxy: { "/v1": "http://localhost:8080", "/graphql": "http://localhost:8080" } },
  test: { environment: "jsdom", setupFiles: ["./src/testing/setup.ts"], globals: true },
});
