import { expect, test } from '@playwright/test'

/**
 * Analytics: gráficos derivados SOLO de la DB (states, fechas, intentos).
 * Nada de views/engagement inventados — la UI lo declara como 🧭 BACKLOG.
 *
 * Este spec es el primero en orden alfabético, así que el dev-mock todavía
 * está en su seed: 3 publicaciones (1 published / 1 pending / 1 rate limit) y
 * 4 jobs (publish+process done, discovery running, poll_publications queued).
 */
const EMPTY_OVERVIEW = {
  sources: {},
  source_clips: {},
  videos: { incoming: 0, processing: 0, completed: 0, failed: 0 },
  clips: { processing: 0, completed: 0, failed: 0 },
  publications: {},
  jobs: {},
}

const EMPTY_PAGE = {
  data: [],
  pagination: { page: 1, pageSize: 100, total: 0, pageCount: 1, hasNext: false, hasPrev: false },
}

test.describe('Analytics', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/analytics')
    await expect(page.getByRole('heading', { level: 1, name: 'Analytics' })).toBeVisible()
  })

  test('declara que las métricas de plataforma siguen siendo BACKLOG', async ({ page }) => {
    // El shell también usa role="status" (badge de API), así que se acota por texto.
    const banner = page.getByRole('status').filter({ hasText: 'métricas de plataforma' })
    await expect(banner).toContainText('métricas de plataforma (views, engagement)')
    await expect(banner).toContainText('BACKLOG')
    await expect(banner).toContainText('sin simular nada')
  })

  test('grafica publicaciones por plataforma y estado', async ({ page }) => {
    const card = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Publicaciones por plataforma y estado' }) })

    await expect(card.getByRole('img', { name: 'Publicaciones por plataforma y estado' })).toBeVisible()
    // Totales derivados del overview del backend, no datos inventados.
    await expect(card.locator('p.text-xs')).toHaveText('youtube: 1 · meta: 2')
  })

  test('grafica jobs por tipo y estado', async ({ page }) => {
    const card = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Jobs por tipo y estado' }) })

    await expect(card.getByRole('img', { name: 'Jobs por tipo y estado' })).toBeVisible()
    await expect(card.locator('p.text-xs')).toContainText('Discovery: 1')
    await expect(card.locator('p.text-xs')).toContainText('Poll pubs: 1')
  })

  test('cuenta las publicadas por día usando published_at', async ({ page }) => {
    const card = page.locator('section').filter({ has: page.getByRole('heading', { name: /Publicadas por d.a/ }) })

    await expect(card.getByRole('img', { name: 'Publicaciones publicadas por día' })).toBeVisible()
    await expect(card.locator('p.text-xs')).toHaveText(/^1 publicadas en los últimos 30 días/)
  })

  test('sin datos muestra los tres estados vacíos', async ({ page }) => {
    await page.route('**/api/system/overview', (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(EMPTY_OVERVIEW) }),
    )
    await page.route(/\/api\/publications(\?|$)/, (route) =>
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(EMPTY_PAGE) }),
    )

    await page.goto('/analytics')

    await expect(page.getByText('Sin publicaciones todavía')).toBeVisible()
    await expect(page.getByText('Sin jobs todavía')).toBeVisible()
    await expect(page.getByText('Aún no hay publicaciones con fecha')).toBeVisible()
    await expect(page.getByRole('img')).toHaveCount(0)
  })

  test('un fallo de la API muestra el error con reintento', async ({ page }) => {
    await page.route('**/api/system/overview', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'INTERNAL', message: 'boom', details: null } }),
      }),
    )

    await page.goto('/analytics')

    await expect(page.getByRole('alert').first()).toBeVisible()
    await expect(page.getByRole('button', { name: 'Reintentar' }).first()).toBeVisible()
  })
})