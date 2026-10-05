import { expect, test, type Page } from '@playwright/test'

const rowOf = (page: Page, channel: string) =>
  page.getByRole('row').filter({ hasText: channel })

test.describe('Canales', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/channels')
    await expect(page.getByRole('heading', { level: 1, name: 'Channels' })).toBeVisible()
    await expect(rowOf(page, 'illojuan')).toBeVisible()
  })

  test('lista los canales configurados', async ({ page }) => {
    await expect(rowOf(page, 'illojuan')).toBeVisible()
    await expect(rowOf(page, 'Rivers_gg')).toBeVisible()
    await expect(page.getByText(/canal\(es\) configurado\(s\)/)).toBeVisible()
  })

  test('crea un canal y lo elimina (CRUD completo)', async ({ page }) => {
    await page.getByRole('button', { name: 'Agregar canal' }).first().click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Agregar canal' })).toBeVisible()

    await dialog.getByLabel(/channel id \/ url/i).fill('e2e-canal')
    await dialog.getByLabel('Nombre', { exact: true }).fill('Canal E2E')
    await dialog.getByRole('button', { name: 'Agregar', exact: true }).click()

    await expect(dialog).toHaveCount(0)
    await expect(rowOf(page, 'Canal E2E')).toBeVisible()

    await page.getByRole('button', { name: 'Eliminar Canal E2E' }).click()
    await page.getByRole('dialog').getByRole('button', { name: 'Eliminar', exact: true }).click()

    await expect(rowOf(page, 'Canal E2E')).toHaveCount(0)
    await expect(rowOf(page, 'illojuan')).toBeVisible()
  })

  test('el formulario valida los campos obligatorios', async ({ page }) => {
    await page.getByRole('button', { name: 'Agregar canal' }).first().click()

    const dialog = page.getByRole('dialog')
    await dialog.getByRole('button', { name: 'Agregar', exact: true }).click()

    await expect(dialog.getByRole('alert')).toHaveCount(2)
    await expect(dialog).toBeVisible()
  })

  test('activa y desactiva un canal', async ({ page }) => {
    await expect(rowOf(page, 'illojuan').getByText('activo')).toBeVisible()

    await page.getByRole('button', { name: 'Desactivar illojuan' }).click()
    await expect(rowOf(page, 'illojuan').getByText('inactivo')).toBeVisible()

    await page.getByRole('button', { name: 'Activar illojuan' }).click()
    await expect(rowOf(page, 'illojuan').getByText('activo')).toBeVisible()
  })

  test('editar un canal precarga los valores actuales y se puede cancelar', async ({ page }) => {
    await page.getByRole('button', { name: 'Editar illojuan' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: /editar canal illojuan/i })).toBeVisible()
    await expect(dialog.getByLabel(/channel id \/ url/i)).toHaveValue('illojuan')
    await expect(dialog.getByLabel('Nombre', { exact: true })).toHaveValue('illojuan')

    await dialog.getByRole('button', { name: 'Cancelar' }).click()
    await expect(dialog).toHaveCount(0)
  })

  test('sincronizar encola discovery y el canal gana un clip', async ({ page }) => {
    await page.goto('/clips')
    await page.getByLabel('Canal', { exact: true }).selectOption({ label: 'illojuan' })
    await expect(page.locator('tbody tr').first()).toBeVisible()
    const before = await page.locator('tbody tr').count()

    await page.goto('/channels')
    await page.getByRole('button', { name: 'Sincronizar illojuan' }).click()

    await page.goto('/clips')
    await page.getByLabel('Canal', { exact: true }).selectOption({ label: 'illojuan' })
    await expect(page.locator('tbody tr')).toHaveCount(before + 1)
  })

  test('el diálogo de eliminar se puede cancelar', async ({ page }) => {
    await page.getByRole('button', { name: 'Eliminar illojuan' }).click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Eliminar canal' })).toBeVisible()
    await dialog.getByRole('button', { name: 'Cancelar' }).click()

    await expect(dialog).toHaveCount(0)
    await expect(rowOf(page, 'illojuan')).toBeVisible()
  })
})