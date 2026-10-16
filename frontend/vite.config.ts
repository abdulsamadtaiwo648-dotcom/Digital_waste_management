import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { fileURLToPath } from 'node:url'

const fromRoot = (file: string) => fileURLToPath(new URL(file, import.meta.url))

export default defineConfig({
  plugins: [react()],
  server: { host: '127.0.0.1', port: 5173 },
  build: { rollupOptions: { input: { resident: fromRoot('./index.html'), management: fromRoot('./management.html'), admin: fromRoot('./admin.html') } } },
})
