import { expect, test } from '@playwright/test'

test.describe('Dashboard', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/')
    await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
  })

  test('muestra los KPIs que el backend sí sabe contar', async ({ page }) => {
    const metrics = page.locator('section').filter({ has: page.getByRole('heading', { name: /m.tricas del pipeline/i }) })

    for (const label of ['Canales activos', 'Detectados', 'Completados', 'Publicados', 'Pendientes public.', 'Jobs en error']) {
      await expect(metrics.getByText(label, { exact: true })).toBeVisible()
    }

    // Cada KPI trae un valor numérico (nunca '—' ni un views inventado).
    const cards = metrics.locator('section')
    await expect(cards).toHaveCount(6)
    for (let i = 0; i < 6; i++) {
      await expect(cards.nth(i).locator('p').last()).toHaveText(/\d+/)
    }
  })

  test('muestra el estado del sistema (health)', async ({ page }) => {
    const status = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Estado del sistema' }) })

    await expect(status.getByText('Worker')).toBeVisible()
    await expect(status.getByText('running')).toBeVisible()
    await expect(status.getByText('Schema DB')).toBeVisible()
    await expect(status.locator('p.tabular-nums')).toHaveText(/\d+/)
  })

  test('lista los trabajos recientes con su estado', async ({ page }) => {
    const queue = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Trabajos recientes' }) })

    await expect(queue.getByRole('listitem').first()).toBeVisible()
    await expect(queue.getByText(/ref #/).first()).toBeVisible()
    await expect(queue.getByText('poll_publications')).toBeVisible()
  })

  test('lista los clips recientes y advierte que no hay métricas', async ({ page }) => {
    const clips = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Clips recientes' }) })

    await expect(clips.getByRole('list').getByRole('link').first()).toBeVisible()
    await expect(clips.getByText(/requieren el backend de m.tricas/i)).toBeVisible()
  })

  test('un clip del dashboard navega a su detalle', async ({ page }) => {
    const clips = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Clips recientes' }) })
    await clips.getByRole('list').getByRole('link').first().click()

    await expect(page).toHaveURL(/\/clips\/\d+$/)
    await expect(page.getByRole('link', { name: /volver a clips/i })).toBeVisible()
  })

  test('un endpoint REQUIRED ausente se marca como bloqueado', async ({ page }) => {
    await page.route(/\/api\/health$/, (route) =>
      route.fulfill({
        status: 404,
        contentType: 'application/json',
        body: JSON.stringify({
          error: {
            code: 'NOT_FOUND',
            message: 'FRONTEND REQUIRES BACKEND ENDPOINT: /api/health no implementado',
            details: null,
          },
        }),
      }),
    )

    await page.goto('/')
    await expect(page.getByText('1 endpoint REQUIRED pendiente')).toBeVisible()
  })

  test('un fallo de la API muestra el estado de error con reintento', async ({ page }) => {
    await page.route('**/api/system/overview', (route) =>
      route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: { code: 'INTERNAL', message: 'boom', details: null } }),
      }),
    )

    await page.goto('/')
    await expect(page.getByRole('alert').first()).toBeVisible()
    await expect(page.getByRole('button', { name: 'Reintentar' }).first()).toBeVisible()
  })
})