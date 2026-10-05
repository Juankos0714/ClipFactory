import { expect, test } from '@playwright/test'

const STORAGE_KEY = 'clipfactory.automation.drafts.v1'

test.describe('Automatización (borradores locales)', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/automation')
    await expect(page.getByRole('heading', { level: 1, name: 'Automation' })).toBeVisible()
  })

  test('avisa que el backend todavía no expone /api/automations', async ({ page }) => {
    await expect(page.getByText('/api/automations')).toBeVisible()
    await expect(page.getByText(/borradores locales/i)).toBeVisible()
    await expect(page.getByText('Sin reglas de automatizaci')).toBeVisible()
  })

  test('crea un borrador CUÁNDO → ENTONCES y lo persiste en localStorage', async ({ page }) => {
    await page.getByRole('button', { name: 'Nueva regla' }).first().click()

    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: /nueva regla/i })).toBeVisible()

    await dialog.getByLabel('Nombre', { exact: true }).fill('Borrador E2E')
    await dialog.getByLabel(/cu.ndo/i).selectOption('clip_detected')
    await dialog.getByLabel('Plataforma', { exact: true }).selectOption('twitch')
    await dialog.getByLabel(/duraci.n m.nima/i).fill('120')
    await dialog.getByLabel(/entonces/i).selectOption('download')
    await dialog.getByRole('button', { name: 'Crear regla' }).click()

    await expect(dialog).toHaveCount(0)
    await expect(page.getByText('Borrador E2E')).toBeVisible()
    await expect(page.getByText('pendiente de backend')).toBeVisible()
    await expect(page.getByText('1 borrador guardado localmente')).toBeVisible()

    const stored = await page.evaluate((key) => window.localStorage.getItem(key), STORAGE_KEY)
    expect(stored).toContain('Borrador E2E')
    expect(JSON.parse(stored ?? '[]')).toHaveLength(1)
  })

  test('el formulario rechaza condiciones inválidas', async ({ page }) => {
    await page.getByRole('button', { name: 'Nueva regla' }).first().click()

    const dialog = page.getByRole('dialog')
    await dialog.getByLabel(/duraci.n m.nima/i).fill('0')
    await dialog.getByRole('button', { name: 'Crear regla' }).click()

    await expect(dialog.getByRole('alert')).toHaveCount(2)
    await expect(dialog).toBeVisible()
  })

  test('pausa y reactiva un borrador', async ({ page }) => {
    await page.getByRole('button', { name: 'Nueva regla' }).first().click()
    await page.getByRole('dialog').getByLabel('Nombre', { exact: true }).fill('Toggle E2E')
    await page.getByRole('dialog').getByRole('button', { name: 'Crear regla' }).click()
    await expect(page.getByText('Toggle E2E')).toBeVisible()

    await page.getByRole('switch', { name: /pausar borrador/i }).click()
    await expect(page.getByText('borrador pausado')).toBeVisible()

    await page.getByRole('switch', { name: /activar borrador/i }).click()
    await expect(page.getByText('borrador activo')).toBeVisible()
  })

  test('elimina un borrador con confirmación', async ({ page }) => {
    await page.getByRole('button', { name: 'Nueva regla' }).first().click()
    await page.getByRole('dialog').getByLabel('Nombre', { exact: true }).fill('Borrador a borrar')
    await page.getByRole('dialog').getByRole('button', { name: 'Crear regla' }).click()
    await expect(page.getByText('Borrador a borrar')).toBeVisible()

    await page.getByRole('button', { name: /eliminar borrador/i }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog.getByRole('heading', { name: 'Eliminar borrador' })).toBeVisible()
    await dialog.getByRole('button', { name: 'Eliminar', exact: true }).click()

    await expect(page.getByText('Borrador a borrar')).toHaveCount(0)
    await expect(page.getByText('Sin reglas de automatizaci')).toBeVisible()
  })

  test('un borrador sobrevive a una recarga', async ({ page }) => {
    await page.getByRole('button', { name: 'Nueva regla' }).first().click()
    await page.getByRole('dialog').getByLabel('Nombre', { exact: true }).fill('Persistente')
    await page.getByRole('dialog').getByRole('button', { name: 'Crear regla' }).click()
    await expect(page.getByText('Persistente')).toBeVisible()

    await page.reload()

    await expect(page.getByText('Persistente')).toBeVisible()
  })
})