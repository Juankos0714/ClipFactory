// @vitest-environment jsdom
import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { fireEvent } from '@testing-library/react'
import { VideoPlayer } from './video-player'

describe('VideoPlayer', () => {
  it('renderiza el <video> con la fuente y el poster', () => {
    render(<VideoPlayer src="/api/clips/1/video" poster="/api/clips/1/thumbnail" title="Clip 1" />)
    const video = screen.getByLabelText<HTMLVideoElement>('Clip 1')
    expect(video).toBeInTheDocument()
    expect(video.poster).toContain('/api/clips/1/thumbnail')
    expect(video.querySelector('source')?.getAttribute('src')).toBe('/api/clips/1/video')
  })

  it('muestra el placeholder honesto cuando no hay src (endpoint no disponible)', () => {
    render(<VideoPlayer src={null} title="Clip 1" />)
    expect(screen.getByText('No hay archivo de video disponible.')).toBeInTheDocument()
    expect(screen.queryByLabelText('Clip 1')).not.toBeInTheDocument()
  })

  it('tras un error de reproducción ofrece Reintentar y lo reintenta', () => {
    render(<VideoPlayer src="/api/clips/1/video" title="Clip 1" />)
    const video = screen.getByLabelText('Clip 1')
    fireEvent.error(video)

    expect(screen.getByText('No se pudo reproducir el video.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'Reintentar' }))
    expect(screen.getByLabelText('Clip 1')).toBeInTheDocument()
  })
})