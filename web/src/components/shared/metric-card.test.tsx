// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MetricCard } from './metric-card'

describe('MetricCard', () => {
  it('renderiza label y valor', () => {
    render(<MetricCard label="Clips completados" value={123} />)
    expect(screen.getByText('Clips completados')).toBeInTheDocument()
    expect(screen.getByText('123')).toBeInTheDocument()
  })

  it('muestra el hint cuando se pasa', () => {
    render(<MetricCard label="Views" value="1000" hint="+10% esta semana" />)
    expect(screen.getByText('+10% esta semana')).toBeInTheDocument()
  })

  it('en loading muestra skeleton y no el valor', () => {
    const { container } = render(<MetricCard label="Jobs" value={5} loading />)
    expect(container.querySelector('[aria-busy="true"], .animate-pulse')).not.toBeNull()
    expect(screen.queryByText('5')).not.toBeInTheDocument()
  })

  it('usa el guion largo cuando el valor es null', () => {
    render(<MetricCard label="Publications" value={null} />)
    expect(screen.getByText('—')).toBeInTheDocument()
  })
})