import { defineConfig as defineVitestConfig } from 'vitest/config'
import { fileURLToPath, URL } from 'node:url'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { loadEnv } from 'vite'
import { devMockPlugin } from './dev-mock'

export default defineVitestConfig(({ mode }) => {
  // Env tipado del frontend (web/.env). Sin secretos (AGENTS.md §5).
  const env = loadEnv(mode, process.cwd(), '')
  // Base URL pública del backend, SIN el prefijo `/api` (el contrato lo incluye).
  // Dev: vacío → proxy `/api` hacia el server Go aditivo.
  const apiUrl = env.VITE_API_URL ?? ''
  // Mock 12v. del contrato (solo dev): VITE_MOCK=true (ver docs de desarrollo).
  const mockPlugins = env.VITE_MOCK === 'true' ? [devMockPlugin()] : []

  return {
    plugins: [react(), tailwindcss(), ...mockPlugins],
    resolve: {
      alias: {
        '@': fileURLToPath(new URL('./src', import.meta.url)),
      },
    },
    server: {
      port: 5173,
      proxy: {
        '/api': {
          target: apiUrl || 'http://localhost:8080',
          changeOrigin: true,
        },
      },
    },
    test: {
      environment: 'node',
      coverage: {
        provider: 'v8',
        include: ['src/lib/**', 'src/services/**'],
      },
    },
  }
})