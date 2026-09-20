// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { render, screen, waitFor } from '@testing-library/react'
import { fireEvent } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { LoginPage } from './login-page'

const { loginMock } = vi.hoisted(() => ({ loginMock: vi.fn() }))

let active = false

vi.mock('@/providers/AuthProvider', () => ({
  useAuth: () => ({
    sessionActive: active,
    login: (token: string) => {
      loginMock(token)
      active = true
    },
  }),
}))

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/login']}>
      <LoginPage />
    </MemoryRouter>,
  )
}

function submitForm() {
  const form = document.querySelector('form')
  if (!form) throw new Error('No hay <form> renderizada')
  fireEvent.submit(form)
}

describe('LoginPage', () => {
  it('muestra el título y un campo de token sin estado inicial de sesión', () => {
    renderPage()
    expect(screen.getByRole('heading', { name: /Autenticación de operador/ })).toBeInTheDocument()
    expect(screen.getByLabelText('Token de API')).toBeInTheDocument()
  })

  it('valida token vacío y no llama a login', async () => {
    renderPage()
    submitForm()
    expect(await screen.findByText('Ingresá el token de operador')).toBeInTheDocument()
    expect(loginMock).not.toHaveBeenCalled()
  })

  it('con token guarda la sesión y sale de la página de login', async () => {
    renderPage()
    fireEvent.change(screen.getByLabelText('Token de API'), { target: { value: 'abc-123' } })
    submitForm()
    await waitFor(() => expect(loginMock).toHaveBeenCalledWith('abc-123'))
    await waitFor(() => expect(screen.queryByLabelText('Token de API')).not.toBeInTheDocument())
  })
})