/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// https://vite.dev/config/
export default defineConfig({
  plugins: [vue()],
  server: {
    // Dev-only: the Go backend runs on :8080 (`go run ./cmd/rhino serve`)
    // while Vite serves the frontend with hot reload on its own port. In
    // production the built dist/ is embedded straight into the Go binary
    // (see backend/assets.go) and this proxy doesn't apply.
    proxy: {
      '/api': 'http://localhost:8080',
    },
  },
  test: {
    // Tests live in tests/, not next to components — see
    // plans/web_portal.md §3 "Tests: frontend/tests/".
    include: ['tests/**/*.test.ts'],
    environment: 'jsdom',
    setupFiles: ['./tests/setup.ts'],
  },
})
