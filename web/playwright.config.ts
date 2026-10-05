import { defineConfig, devices } from '@playwright/test'

/**
 * E2E del frontend contra el dev-mock del contrato (web/dev-mock.ts), que corre
 * como middleware de Vite con VITE_MOCK=true. Así los flujos son deterministas
 * y no dependen del server Go ni de credenciales reales.
 *
 *   npm run test:e2e        # headless
 *   npm run test:e2e:ui     # inspector
 *   npm run test:e2e:headed # con navegador visible
 */
export default defineConfig({
  testDir: './e2e',
  // El dev-mock tiene una base en memoria compartida por el server de Vite: los
  // tests se ejecutan en serie para que las mutaciones de un flujo no rompan otro.
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  timeout: 30_000,
  expect: { timeout: 7_000 },
  use: {
    baseURL: 'http://127.0.0.1:5173',
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    // `npm run dev` (no `vite` directo): en Windows el binario de node_modules/.bin
    // solo está en el PATH cuando lo invoca npm. `--host 127.0.0.1` evita que
    // vite binds solo a ::1 y Playwright no lo alcance por IPv4.
    command: 'npm run dev -- --host 127.0.0.1 --port 5173 --strictPort',
    url: 'http://127.0.0.1:5173',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    // El mock del contrato (web/dev-mock.ts) se activa solo en `serve`.
    env: { VITE_MOCK: 'true' },
  },
})