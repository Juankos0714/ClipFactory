import { expect, test, type Page } from '@playwright/test'

const rowOf = (page: Page, clipTitle: string) => page.getByRole('row').filter({ hasText: clipTitle })

test.describe('Publicaciones', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/publications')
    await expect(page.getByRole('heading', { level: 1, name: 'Publications' })).toBeVisible()
    await expect(page.locator('tbody tr').first()).toBeVisible()
  })

  test('lista las publicaciones con destino y estado', async ({ page }) => {
    const table = page.getByRole('table')

    await expect(table.getByText('YouTube')).toBeVisible()
    // Hay dos publicaciones en Meta (una pending, otra en rate limit).
    await expect(table.getByText('Meta').first()).toBeVisible()
    await expect(table.getByText('published')).toBeVisible()
    await expect(table.getByText('waiting_rate_limit')).toBeVisible()
    await expect(table.getByText('rate limit exceeded')).toBeVisible()
  })

  test('filtra por plataforma', async ({ page }) => {
    await page.getByLabel('Plataforma', { exact: true }).selectOption('youtube')

    await expect(page.locator('tbody tr')).toHaveCount(1)
    await expect(page.locator('tbody tr')).toContainText('YouTube')
  })

  test('filtra por estado', async ({ page }) => {
    await page.getByLabel(/estado/i).selectOption('pending')

    await expect(page.locator('tbody tr')).toHaveCount(1)
    await expect(page.locator('tbody tr')).toContainText('pending')
  })

  test('un filtro sin resultados muestra el estado vacío', async ({ page }) => {
    await page.getByLabel('Plataforma', { exact: true }).selectOption('youtube')
    await page.getByLabel(/estado/i).selectOption('waiting_rate_limit')

    await expect(page.getByText('Sin publicaciones con estos filtros')).toBeVisible()
    await page.getByRole('form', { name: 'Filtros de publicaciones' }).getByRole('button', { name: 'Limpiar filtros' }).click()
    await expect(page.locator('tbody tr').first()).toBeVisible()
  })

  test('cancelar una publicación en cola pide confirmación', async ({ page }) => {
    // El locator NO filtra por 'pending': tras cancelar el estado pasa a 'failed'.
    const row = rowOf(page, 'Reaccionando a mi peor clip').filter({ hasText: 'Meta' })
    await expect(row.getByText('pending')).toBeVisible()

    await row.getByRole('button', { name: 'Cancelar' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: /cancelar publicaci/i })).toBeVisible()
    await expect(dialog.getByText(/dead-letter/i)).toBeVisible()

    await dialog.getByRole('button', { name: 'Cancelar', exact: true }).last().click()

    await expect(dialog).toHaveCount(0)
    await expect(row.getByText('failed')).toBeVisible()
    await expect(row).toContainText('cancelado por el operador')
  })

  test('una publicación publicada ofrece el link externo y ninguna acción', async ({ page }) => {
    const row = page.getByRole('row').filter({ hasText: 'published' })
    await expect(row.getByRole('link', { name: 'Ver' })).toHaveAttribute('href', 'https://youtu.be/dQw4w9WgXcQ')
    await expect(row.getByRole('button')).toHaveCount(0)
  })

  test('la publicación en rate limit se puede cancelar', async ({ page }) => {
    const row = page.getByRole('row').filter({ hasText: 'waiting_rate_limit' })
    await row.getByRole('button', { name: 'Cancelar' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Cancelar', exact: true }).first().click()

    await expect(page.getByRole('dialog')).toHaveCount(0)
    await expect(row.getByText('waiting_rate_limit')).toBeVisible()
  })
})