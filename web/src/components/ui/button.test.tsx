// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { fireEvent } from '@testing-library/react'
import { Button } from './button'

describe('Button', () => {
  it('renderiza children y dispara onClick', () => {
    const onClick = vi.fn()
    render(
      <Button onClick={onClick}>
        Encolar
      </Button>,
    )
    fireEvent.click(screen.getByRole('button', { name: 'Encolar' }))
    expect(onClick).toHaveBeenCalledTimes(1)
  })

  it('con loading: queda disabled y expone aria-busy', () => {
    render(
      <Button loading>
        Guardando
      </Button>,
    )
    const button = screen.getByRole('button', { name: 'Guardando' })
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('aria-busy', 'true')
  })

  it('variant secondary -> ghost cambia las clases de estilo', () => {
    const { rerender } = render(<Button variant="secondary">A</Button>)
    expect(screen.getByRole('button')).toHaveClass('bg-surface-elevated')
    rerender(<Button variant="ghost">A</Button>)
    expect(screen.getByRole('button')).not.toHaveClass('bg-surface-elevated')
  })
})