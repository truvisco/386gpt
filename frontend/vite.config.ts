import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

import { cloudflare } from "@cloudflare/vite-plugin";

// https://vite.dev/config/
export default defineConfig({
  plugins: [react(), cloudflare()],
  server: {
    host: '127.0.0.1',
    port: 21350,
    strictPort: true,
    allowedHosts: ['386gpt.dev14.portnumber53.com'],
    proxy: {
      '/api': 'http://127.0.0.1:21351',
      '/ws': { target: 'ws://127.0.0.1:21351', ws: true },
    },
  },
})
