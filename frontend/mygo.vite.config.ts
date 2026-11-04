import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  define: { "process.env.NODE_ENV": JSON.stringify("production") },
  build: {
    outDir: "mygo-web/assets", emptyOutDir: true, copyPublicDir: false,
    lib: { entry: "src/mygo/PianoPage.tsx", formats: ["es"], fileName: () => "piano.js", cssFileName: "piano" },
  },
});
