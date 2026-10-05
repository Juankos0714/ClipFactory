import { expect, test } from '@playwright/test'

const SECTIONS = [
  { link: 'Channels', url: '/channels', title: 'Channels' },
  { link: 'Clips', url: '/clips', title: 'Clips' },
  { link: 'Production', url: '/production', title: 'Production' },
  { link: 'Queue', url: '/production/queue', title: 'Queue' },
  { link: 'Automation', url: '/automation', title: 'Automation' },
  { link: 'Publications', url: '/publications', title: 'Publications' },
  { link: 'Analytics', url: '/analytics', title: 'Analytics' },
  { link: 'Settings', url: '/settings', title: 'Settings' },
]

test.describe('Navegación', () => {
  test('cada sección del sidebar navega y cambia el título', async ({ page }) => {
    await page.goto('/')
    const nav = page.getByRole('navigation', { name: 'Principal' })

    for (const section of SECTIONS) {
      await nav.getByRole('link', { name: section.link, exact: true }).click()
      await expect(page).toHaveURL(section.url)
      await expect(page.getByRole('heading', { level: 1, name: section.title })).toBeVisible()
    }
  })

  test('el enlace de salto al contenido aparece al tabular', async ({ page }) => {
    await page.goto('/')
    await page.keyboard.press('Tab')

    const skip = page.getByRole('link', { name: 'Saltar al contenido' })
    await expect(skip).toBeFocused()
    await skip.press('Enter')
    await expect(page.locator('#main')).toBeVisible()
  })

  test('una ruta inexistente muestra la pantalla de 404 con salida', async ({ page }) => {
    await page.goto('/no-existe')

    await expect(page.locator('#main').getByText('404')).toBeVisible()
    await page.locator('#main').getByRole('link', { name: /dashboard/i }).click()
    await expect(page).toHaveURL('/')
  })
})

test.describe('Navegación móvil', () => {
  test.use({ viewport: { width: 390, height: 844 } })

  test('el drawer abre, navega y se cierra', async ({ page }) => {
    await page.goto('/')

    const menuButton = page.getByRole('button', { name: 'Abrir menú' })
    await expect(menuButton).toBeVisible()
    await menuButton.click()

    const drawer = page.getByRole('dialog', { name: 'Navegación' })
    await expect(drawer).toBeVisible()

    await drawer.getByRole('link', { name: 'Channels', exact: true }).click()

    await expect(page).toHaveURL('/channels')
    await expect(drawer).toHaveCount(0)
  })

  test('el drawer se cierra con el botón de cerrar y con Escape', async ({ page }) => {
    await page.goto('/')

    await page.getByRole('button', { name: 'Abrir menú' }).click()
    await expect(page.getByRole('dialog', { name: 'Navegación' })).toBeVisible()
    await page.getByRole('button', { name: 'Cerrar menú' }).click()
    await expect(page.getByRole('dialog', { name: 'Navegación' })).toHaveCount(0)

    await page.getByRole('button', { name: 'Abrir menú' }).click()
    await expect(page.getByRole('dialog', { name: 'Navegación' })).toBeVisible()
    await page.keyboard.press('Escape')
    await expect(page.getByRole('dialog', { name: 'Navegación' })).toHaveCount(0)
  })
})