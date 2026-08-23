import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

const buildVersion = process.env.PROCTOR_BUILD_VERSION?.trim() || "dev";
const buildCommit = process.env.PROCTOR_BUILD_COMMIT?.trim() || "unknown";

function proctorBuildManifest(): Plugin {
  return {
    name: "proctor-build-manifest",
    generateBundle() {
      this.emitFile({
        type: "asset",
        fileName: "webapp-build.json",
        source: `${JSON.stringify({
          schema_version: 1,
          version: buildVersion,
          commit: buildCommit,
        })}\n`,
      });
    },
  };
}

export default defineConfig({
  base: "/",
  plugins: [react(), proctorBuildManifest()],
  build: {
    assetsDir: "assets",
    sourcemap: false,
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    strictPort: true,
    proxy: {
      "/api": {
        target: process.env.PROCTOR_SERVER_DEV_URL || "http://127.0.0.1:8065",
        changeOrigin: false,
      },
    },
  },
});
