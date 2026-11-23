import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { fileURLToPath, URL } from 'node:url'

export default defineConfig({
  plugins: [vue()],
  build: {
    outDir: fileURLToPath(new URL('./dist', import.meta.url)),
    lib: { entry: fileURLToPath(new URL('./src/index.ts', import.meta.url)), formats: ['es'], fileName: 'newapi-ui' },
    rollupOptions: { external: ['vue'] }
  }
})
