import { expect, test, type Page } from '@playwright/test'

const rowOf = (page: Page, title: string) =>
  page.getByRole('row').filter({ hasText: title })

test.describe('Clips', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/clips')
    await expect(page.getByRole('heading', { level: 1, name: 'Clips' })).toBeVisible()
    await expect(page.locator('tbody tr').first()).toBeVisible()
  })

  test('lista los clips detectados con canal, duración y estado', async ({ page }) => {
    await expect(rowOf(page, 'El rage masivo con el chat')).toBeVisible()
    await expect(rowOf(page, 'Torneo annus final')).toBeVisible()
    await expect(page.getByText('Clips detectados por el pipeline')).toBeVisible()
    await expect(page.getByRole('columnheader', { name: 'Duraci' })).toBeVisible()
  })

  test('filtra por plataforma', async ({ page }) => {
    await page.getByLabel('Plataforma', { exact: true }).selectOption('kick')

    await expect(page.locator('tbody tr').first()).toBeVisible()
    const rows = page.locator('tbody tr')
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
    for (let i = 0; i < count; i++) {
      await expect(rows.nth(i)).toContainText('kick')
    }
  })

  test('filtra por canal', async ({ page }) => {
    await page.getByLabel('Canal', { exact: true }).selectOption({ label: 'illojuan' })

    const rows = page.locator('tbody tr')
    await expect(rows.first()).toBeVisible()
    const count = await rows.count()
    for (let i = 0; i < count; i++) {
      await expect(rows.nth(i)).toContainText('illojuan')
    }
  })

  test('un filtro sin resultados muestra el estado vacío y permite limpiar', async ({ page }) => {
    await page.getByLabel('Canal', { exact: true }).selectOption({ label: 'illojuan' })
    await page.getByLabel('Plataforma', { exact: true }).selectOption('kick')

    await expect(page.getByText('No hay clips con estos filtros')).toBeVisible()
    await expect(page.locator('tbody tr')).toHaveCount(0)

    await page.getByRole('form', { name: 'Filtros de clips' }).getByRole('button', { name: 'Limpiar filtros' }).click()
    await expect(page.locator('tbody tr').first()).toBeVisible()
  })

  test('seleccionar un clip detected habilita encolar descarga', async ({ page }) => {
    const enqueue = page.getByRole('button', { name: /encolar descarga/i })
    await expect(enqueue).toBeDisabled()

    const detected = rowOf(page, 'El rage masivo con el chat')
    await expect(detected.getByText('detected')).toBeVisible()
    await page.getByRole('checkbox', { name: /seleccionar el rage masivo/i }).check()

    await expect(page.getByText('1 seleccionado(s)')).toBeVisible()
    await expect(enqueue).toBeEnabled()

    await enqueue.click()
    await expect(detected.getByText('downloaded')).toBeVisible()
    await expect(page.getByText('Clips detectados por el pipeline')).toBeVisible()
  })

  test('seleccionar clips no descargables mantiene el botón bloqueado', async ({ page }) => {
    const enqueue = page.getByRole('button', { name: /encolar descarga/i })

    await page.getByRole('checkbox', { name: /seleccionar el rage masivo/i }).check()
    await page.getByRole('checkbox', { name: /seleccionar reaccionando/i }).check()

    await expect(page.getByText('2 seleccionado(s)')).toBeVisible()
    await expect(enqueue).toBeDisabled()
  })

  test('el detalle del clip muestra pipeline y métricas no disponibles', async ({ page }) => {
    await page.getByRole('row').filter({ hasText: 'Reaccionando a mi peor clip' }).getByRole('link', { name: 'Ver' }).click()

    await expect(page).toHaveURL(/\/clips\/\d+$/)
    await expect(page.getByRole('heading', { name: 'Reaccionando a mi peor clip' })).toBeVisible()
    await expect(page.getByText(/m.tricas de rendimiento/i)).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Pipeline' })).toBeVisible()
    await expect(page.getByText('Archivo de origen')).toBeVisible()
    await expect(page.getByText('Clip vertical 1080')).toBeVisible()
  })

  test('el reproductor degrada honestamente si el archivo no se puede leer', async ({ page }) => {
    await page.goto('/clips/2')

    // El dev-mock no sirve bytes de video: la UI debe avisar y permitir reintentar.
    await expect(page.getByText('No se pudo reproducir el video.')).toBeVisible()
    await page.getByRole('button', { name: 'Reintentar' }).click()
    await expect(page.getByText('No se pudo reproducir el video.')).toBeVisible()
  })

  test('un clip inexistente no rompe la app', async ({ page }) => {
    await page.goto('/clips/999999')

    await expect(page.getByRole('alert')).toBeVisible()
    await page.getByRole('button', { name: 'Reintentar' }).click()
    await expect(page.getByRole('alert')).toBeVisible()
  })
})