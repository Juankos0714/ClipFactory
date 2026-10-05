import { expect, test } from '@playwright/test'

const UNAUTHORIZED = {
  status: 401,
  contentType: 'application/json',
  body: JSON.stringify({ error: { code: 'AUTHENTICATION_REQUIRED', message: 'token de API requerido', details: null } }),
}

/** Solo las llamadas al backend: un glob amplio también cortaría /src/lib/api/*. */
const isApiCall = (url: URL) => url.pathname.startsWith('/api/')

/**
 * Auth de operador (API_CONTRACT §2.4): el server SOLO exige token si tiene
 * CLIPFACTORY_API_TOKEN seteado. El dev-mock no lo exige, así que estos tests
 * reproducen el caso "server exige auth" interceptando /api/* con un 401 real.
 */
test.describe('Auth de operador', () => {
  test('sin token el server abre la app y muestra la API conectada', async ({ page }) => {
    await page.goto('/')

    await expect(page).toHaveURL('/')
    await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()
    await expect(page.getByText('API conectada')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Iniciar sesión' })).toHaveCount(0)
  })

  test('/login valida el token vacío', async ({ page }) => {
    await page.goto('/login')

    await expect(page.getByRole('heading', { name: /autenticaci/i })).toBeVisible()
    await page.getByRole('button', { name: /iniciar sesi/i }).click()

    await expect(page.getByRole('alert')).toContainText(/token de operador/i)
    await expect(page).toHaveURL('/login')
  })

  test('un token válido guarda la sesión en sessionStorage y entra al dashboard', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel(/token de API/i).fill('e2e-token')
    await page.getByRole('button', { name: /iniciar sesi/i }).click()

    await expect(page).toHaveURL('/')
    await expect(page.getByRole('heading', { level: 1, name: 'Dashboard' })).toBeVisible()

    const stored = await page.evaluate(() => sessionStorage.getItem('clipfactory.auth.token.v1'))
    expect(stored).toBe('e2e-token')
  })

  test('con sesión activa, /login redirige al dashboard', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel(/token de API/i).fill('e2e-token')
    await page.getByRole('button', { name: /iniciar sesi/i }).click()
    await expect(page).toHaveURL('/')

    await page.goto('/login')
    await expect(page).toHaveURL('/')
  })
})

test.describe('Auth requerido por el server (401)', () => {
  test.beforeEach(async ({ page }) => {
    await page.route(isApiCall, async (route) => {
      await route.fulfill(UNAUTHORIZED)
    })
  })

  test('protege las rutas y redirige a /login', async ({ page }) => {
    await page.goto('/channels')

    await expect(page).toHaveURL('/login')
    await expect(page.getByRole('heading', { name: /autenticaci/i })).toBeVisible()
  })

  test('vuelve a la ruta original tras autenticar', async ({ page }) => {
    await page.goto('/clips')
    await expect(page).toHaveURL('/login')

    await page.getByLabel(/token de API/i).fill('e2e-token')
    await page.getByRole('button', { name: /iniciar sesi/i }).click()

    await expect(page).toHaveURL(/\/clips/)
    // La sesión habilita la ruta aunque el server siga respondiendo 401.
    await expect(page.getByRole('heading', { level: 1, name: 'Clips' })).toBeVisible()
  })

  test('logout vuelve a bloquear el acceso', async ({ page }) => {
    await page.goto('/login')
    await page.getByLabel(/token de API/i).fill('e2e-token')
    await page.getByRole('button', { name: /iniciar sesi/i }).click()
    await expect(page).toHaveURL('/')

    await expect(page.getByText('Token activo')).toBeVisible()
    await page.getByRole('button', { name: 'Salir' }).click()

    await page.goto('/channels')
    await expect(page).toHaveURL('/login')
  })
})