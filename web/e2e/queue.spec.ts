import { expect, test, type Page } from '@playwright/test'

/**
 * Fila de la cola por etiqueta de tipo (la tabla muestra JOB_TYPE_LABELS).
 *
 * El dev-mock es stateful y compartido entre specs (workers: 1), así que otras
 * suites pueden haber creado jobs del mismo tipo: acotamos con `.first()` y nunca
 * filtramos por estado, porque el locator debe seguir resolviendo la misma fila
 * después de una mutación (queued → error → queued).
 */
const rowOf = (page: Page, typeLabel: string) =>
  page.getByRole('row').filter({ has: page.getByRole('cell', { name: typeLabel, exact: true }) })

test.describe('Cola de jobs', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/production/queue')
    await expect(page.getByRole('heading', { level: 1, name: 'Queue' })).toBeVisible()
    await expect(page.getByRole('heading', { name: 'Resumen de la cola' })).toBeVisible()
  })

  test('muestra el resumen por estado y la tabla de jobs', async ({ page }) => {
    const summary = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Resumen de la cola' }) })
    for (const label of ['En cola', 'Ejecutando', 'Errores', 'Finalizados']) {
      await expect(summary.getByText(label, { exact: true })).toBeVisible()
    }

    await expect(rowOf(page, 'Descubrimiento').first()).toBeVisible()
    await expect(rowOf(page, 'Poll publicaciones')).toBeVisible()
    await expect(page.getByRole('columnheader', { name: /intentos/i })).toBeVisible()
    await expect(page.getByRole('columnheader', { name: /referencia/i })).toBeVisible()
  })

  test('filtra por tipo de job', async ({ page }) => {
    await page.getByLabel(/tipo de job/i).selectOption('discovery')

    // Todas las filas visibles deben ser del tipo filtrado (sin depender del total).
    const rows = page.locator('tbody tr')
    await expect(rows.first()).toBeVisible()
    const count = await rows.count()
    expect(count).toBeGreaterThan(0)
    for (let i = 0; i < count; i += 1) {
      await expect(rows.nth(i).getByRole('cell').nth(1)).toHaveText('Descubrimiento')
    }
  })

  test('los jobs finalizados no ofrecen acciones', async ({ page }) => {
    const done = rowOf(page, 'Publicación').filter({ hasText: 'done' })
    await expect(done.getByText('done')).toBeVisible()
    await expect(done.getByRole('button')).toHaveCount(0)
  })

  test('cancelar un job pide confirmación y lo pasa a error', async ({ page }) => {
    const job = rowOf(page, 'Poll publicaciones').first()
    await expect(job.getByText('queued')).toBeVisible()

    await job.getByRole('button', { name: 'Cancelar' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Cancelar job' })).toBeVisible()
    await expect(dialog.getByText(/se marcar. el job/i)).toBeVisible()

    // El diálogo tiene dos botones "Cancelar": el secundario aborta, el de acción confirma.
    await dialog.getByRole('button', { name: 'Cancelar', exact: true }).last().click()

    await expect(dialog).toHaveCount(0)
    await expect(job.getByText('error')).toBeVisible()
    await expect(job).toContainText('cancelado por el operador')
  })

  test('reintentar el job cancelado lo vuelve a encolar', async ({ page }) => {
    const job = rowOf(page, 'Poll publicaciones').first()
    await expect(job.getByText('error')).toBeVisible()

    await job.getByRole('button', { name: 'Reintentar' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Reintentar job' })).toBeVisible()

    await dialog.getByRole('button', { name: 'Reintentar', exact: true }).click()

    await expect(dialog).toHaveCount(0)
    await expect(job.getByText('queued')).toBeVisible()
    await expect(job).not.toContainText('cancelado por el operador')
  })

  test('el diálogo de cancelación se puede cerrar sin confirmar', async ({ page }) => {
    const job = rowOf(page, 'Descubrimiento').filter({ hasText: 'running' })
    await expect(job.getByText('running')).toBeVisible()

    await job.getByRole('button', { name: 'Cancelar' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Cancelar', exact: true }).first().click()

    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(job.getByText('running')).toBeVisible()
  })
})