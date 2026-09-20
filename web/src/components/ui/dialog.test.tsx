// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { fireEvent } from '@testing-library/react'
import { Dialog } from './dialog'

describe('Dialog (a11y)', () => {
  it('cerrado no renderiza nada', () => {
    render(
      <Dialog open={false} onClose={vi.fn()} title="Título">
        <p>Contenido</p>
      </Dialog>,
    )
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('abierto: aria-modal, aria-labelledby, y mueve el foco al diálogo', () => {
    render(
      <Dialog open onClose={vi.fn()} title="Confirmar borrado">
        <p>Contenido</p>
      </Dialog>,
    )
    const dialog = screen.getByRole('dialog', { name: 'Confirmar borrado' })
    expect(dialog).toHaveAttribute('aria-modal', 'true')
    expect(dialog).toHaveFocus()
  })

  it('Esc cierra (llama onClose)', () => {
    const onClose = vi.fn()
    render(
      <Dialog open onClose={onClose} title="Título">
        <p>Contenido</p>
      </Dialog>,
    )
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('restaura el foco en el elemento previo a abrir el diálogo', () => {
    const onClose = vi.fn()
    const { rerender } = render(
      <>
        <button type="button">Ancla de foco</button>
        <Dialog open={false} onClose={onClose} title="Título">
          <p>Contenido</p>
        </Dialog>
      </>,
    )
    const anchor = screen.getByRole('button', { name: 'Ancla de foco' })
    anchor.focus()
    rerender(
      <>
        <button type="button">Ancla de foco</button>
        <Dialog open onClose={onClose} title="Título">
          <p>Contenido</p>
        </Dialog>
      </>,
    )
    expect(screen.getByRole('dialog')).toHaveFocus()

    rerender(
      <>
        <button type="button">Ancla de foco</button>
        <Dialog open={false} onClose={onClose} title="Título">
          <p>Contenido</p>
        </Dialog>
      </>,
    )
    expect(anchor).toHaveFocus()
  })
})