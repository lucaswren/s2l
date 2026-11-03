import { defineConfig } from "vite";
import vue from "@vitejs/plugin-vue";
import Components from "unplugin-vue-components/vite";
import { ElementPlusResolver } from "unplugin-vue-components/resolvers";

export default defineConfig({
  plugins: [
    vue(),
    Components({ resolvers: [ElementPlusResolver()], dts: false }),
  ],
  base: "/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
    assetsDir: "assets",
  },
  server: {
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
});
