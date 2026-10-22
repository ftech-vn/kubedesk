import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  // The frontend is embedded in the app, not downloaded: one chunk is fine.
  build: { chunkSizeWarningLimit: 4096 },
  server: {
    // The app loads this address during `mygo dev` (devUrl in mygo.config.ts).
    port: 5188,
    strictPort: true,
    // The development app and the packaged builds are not the page's.
    watch: { ignored: ["**/.mygo/**", "**/build/**"] },
  },
});
