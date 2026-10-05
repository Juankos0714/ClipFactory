import { expect, test, type Page } from '@playwright/test'

/**
 * Production (FASE 4): resumen del pipeline, filtros y las dos acciones reales
 * (`POST /api/clips/:id/queue-for-process|regenerate-thumbnail`).
 *
 * El dev-mock es stateful y compartido (workers: 1). Cada test elige un clip
 * que ninguna otra suite muta: 'Intentando el speedrun otra vez' (descargado,
 * clip procesando, sin miniatura) y 'Charla nocturna del jueves' (detectado).
 */
const rowOf = (page: Page, title: string) => page.getByRole('row').filter({ hasText: title })

const DOWNLOADED = 'Intentando el speedrun otra vez'
const DETECTED = 'Charla nocturna del jueves'
const PROCESSED = 'Directo de 12h: montaje'

test.describe('Production', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/production')
    await expect(page.getByRole('heading', { level: 1, name: 'Production' })).toBeVisible()
    await expect(page.locator('tbody tr').first()).toBeVisible()
  })

  test('el resumen del pipeline muestra los conteos del overview', async ({ page }) => {
    const summary = page
      .locator('section')
      .filter({ has: page.getByRole('heading', { name: 'Pipeline de publicación' }) })

    for (const label of ['Detectados', 'Descargados', 'Procesando', 'Completados', 'Publicados', 'Jobs en error']) {
      await expect(summary.getByText(label, { exact: true })).toBeVisible()
    }

    // Cada KPI trae un conteo real del backend (nunca '—' con datos cargados).
    const cards = summary.locator('section')
    await expect(cards).toHaveCount(6)
    for (let i = 0; i < 6; i += 1) {
      await expect(cards.nth(i).locator('p').nth(1)).toHaveText(/\d+/)
    }
  })

  test('el resumen enlaza a la cola de trabajos', async ({ page }) => {
    await page.getByRole('link', { name: 'Ver cola de trabajos' }).click()
    await expect(page).toHaveURL('/production/queue')
  })

  test('filtra por plataforma', async ({ page }) => {
    await page.getByLabel('Plataforma', { exact: true }).selectOption('kick')

    const rows = page.locator('tbody tr')
    await expect(rows.first()).toBeVisible()
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
    for (let i = 0; i < count; i += 1) {
      await expect(rows.nth(i)).toContainText('kick')
    }
  })

  test('filtra por estado', async ({ page }) => {
    await page.getByLabel('Estado', { exact: true }).selectOption('skipped')

    await expect(rowOf(page, 'Unboxing la nueva caja')).toContainText('skipped')
    await expect(page.locator('tbody tr')).toHaveCount(1)
  })

  test('un filtro sin resultados muestra el estado vacío y permite limpiar', async ({ page }) => {
    await page.getByLabel('Plataforma', { exact: true }).selectOption('kick')
    await page.getByLabel('Estado', { exact: true }).selectOption('skipped')

    await expect(page.getByText('No hay clips con estos filtros')).toBeVisible()
    await expect(page.locator('tbody tr')).toHaveCount(0)

    await page
      .getByRole('form', { name: 'Filtros del pipeline' })
      .getByRole('button', { name: 'Limpiar filtros' })
      .click()
    await expect(page.locator('tbody tr').first()).toBeVisible()
  })

  test('un clip sin procesar deja las tres acciones bloqueadas', async ({ page }) => {
    const row = rowOf(page, DETECTED)
    await expect(row.getByText('detected')).toBeVisible()

    await expect(row.getByRole('button', { name: 'Ver' })).toBeDisabled()
    await expect(row.getByRole('button', { name: 'Reprocesar' })).toBeDisabled()
    await expect(row.getByRole('button', { name: 'Miniatura' })).toBeDisabled()
  })

  test('un clip descargado habilita preview y reproceso, pero no miniatura', async ({ page }) => {
    const row = rowOf(page, DOWNLOADED)
    await expect(row.getByText('downloaded')).toBeVisible()

    await expect(row.getByRole('button', { name: 'Ver' })).toBeEnabled()
    await expect(row.getByRole('button', { name: 'Reprocesar' })).toBeEnabled()
    await expect(row.getByRole('button', { name: 'Miniatura' })).toBeDisabled()
  })

  test('la vista previa abre el diálogo y Escape lo cierra', async ({ page }) => {
    await rowOf(page, PROCESSED).getByRole('button', { name: 'Ver' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: PROCESSED })).toBeVisible()
    await expect(dialog.getByText('downloaded')).toBeVisible()

    // El dev-mock no sirve bytes de video: el reproductor degrada honestamente.
    await expect(dialog.getByText('No se pudo reproducir el video.')).toBeVisible()

    await page.keyboard.press('Escape')
    await expect(dialog).toHaveCount(0)
  })

  test('las publicaciones recientes solo ofrecen reintento en estado error', async ({ page }) => {
    const card = page
      .locator('section')
      .filter({ has: page.getByRole('heading', { name: 'Publicaciones recientes' }) })

    await expect(card.getByText('Reaccionando a mi peor clip').first()).toBeVisible()
    await expect(card.getByText('published').first()).toBeVisible()
    await expect(card.getByRole('button', { name: 'Reintentar' })).toHaveCount(0)
    await expect(card.getByRole('link', { name: /ver en la plataforma/i })).toHaveAttribute(
      'href',
      'https://youtu.be/dQw4w9WgXcQ',
    )
  })

  test('reprocesar encola un job que después aparece en la cola', async ({ page }) => {
    const enqueued = page.waitForResponse(
      (r) => r.url().includes('/queue-for-process') && r.request().method() === 'POST',
    )
    await rowOf(page, DOWNLOADED).getByRole('button', { name: 'Reprocesar' }).click()

    const res = await enqueued
    expect(res.status()).toBe(200)
    const body = (await res.json()) as { job: { type: string } }
    expect(body.job.type).toBe('process')

    await page.getByRole('link', { name: 'Ver cola de trabajos' }).click()
    await expect(
      page.getByRole('row').filter({ has: page.getByRole('cell', { name: 'Procesado', exact: true }) }).first(),
    ).toBeVisible()
  })

  test('regenerar la miniatura encola un job de thumbnail', async ({ page }) => {
    const row = rowOf(page, PROCESSED)
    await expect(row.getByRole('button', { name: 'Miniatura' })).toBeEnabled()

    const enqueued = page.waitForResponse(
      (r) => r.url().includes('/regenerate-thumbnail') && r.request().method() === 'POST',
    )
    await row.getByRole('button', { name: 'Miniatura' }).click()

    const res = await enqueued
    expect(res.status()).toBe(200)
    const body = (await res.json()) as { job: { type: string } }
    expect(body.job.type).toBe('thumbnail')
  })
})