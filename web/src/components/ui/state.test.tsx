// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { fireEvent } from '@testing-library/react'
import { EmptyState, ErrorState, LoadingState, StateView } from './state'

describe('StateView — los 5 estados (idle/loading/success/empty/error)', () => {
  it('loading: muestra el estado de carga y oculta el contenido', () => {
    render(
      <StateView isLoading error={undefined} isEmpty={false}>
        <p>Contenido</p>
      </StateView>,
    )
    expect(screen.getByRole('status', { name: 'Cargando' })).toBeInTheDocument()
    expect(screen.queryByText('Contenido')).not.toBeInTheDocument()
  })

  it('error: muestre alerta + Reintentar y oculta el contenido', () => {
    const onRetry = vi.fn()
    render(
      <StateView isLoading={false} error={new Error('Fallo la API')} isEmpty={false} onRetry={onRetry}>
        <p>Contenido</p>
      </StateView>,
    )
    expect(screen.getByRole('alert')).toBeInTheDocument()
    expect(screen.getByText(/Fallo la API/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Reintentar' }))
    expect(onRetry).toHaveBeenCalledTimes(1)
    expect(screen.queryByText('Contenido')).not.toBeInTheDocument()
  })

  it('empty: muestra título vacío + acción y oculta el contenido', () => {
    render(
      <StateView
        isLoading={false}
        error={undefined}
        isEmpty
        emptyTitle="Sin clips"
        emptyBody="Probá limpiar filtros."
        emptyAction={<button>Limpiar</button>}
      >
        <p>Contenido</p>
      </StateView>,
    )
    expect(screen.getByText('Sin clips')).toBeInTheDocument()
    expect(screen.getByText('Probá limpiar filtros.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Limpiar' })).toBeInTheDocument()
    expect(screen.queryByText('Contenido')).not.toBeInTheDocument()
  })

  it('success: sin loading/error/empty = renderiza el contenido', () => {
    render(
      <StateView isLoading={false} error={undefined} isEmpty={false}>
        <p>Contenido</p>
      </StateView>,
    )
    expect(screen.getByText('Contenido')).toBeInTheDocument()
  })

  it('LoadingState y EmptyState/ErrorState se usan directamente', () => {
    render(
      <>
        <LoadingState rows={2} />
        <EmptyState title="Vacío" />
        <ErrorState title="Error" />
      </>,
    )
    expect(screen.getByRole('status', { name: 'Cargando' })).toBeInTheDocument()
    expect(screen.getByText('Vacío')).toBeInTheDocument()
    expect(screen.getByText('Error')).toBeInTheDocument()
    expect(screen.getAllByRole('status').length).toBeGreaterThan(0)
  })
})