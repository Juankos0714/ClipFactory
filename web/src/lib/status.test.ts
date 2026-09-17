import { describe, expect, it } from 'vitest'
import { toneClasses, toneFor } from './status'

describe('toneFor', () => {
  it('mapea estados del contrato a tonos', () => {
    expect(toneFor('completed')).toBe('green')
    expect(toneFor('published')).toBe('green')
    expect(toneFor('error')).toBe('red')
    expect(toneFor('failed')).toBe('red')
    expect(toneFor('processing')).toBe('amber')
    expect(toneFor('running')).toBe('amber')
    expect(toneFor('waiting_rate_limit')).toBe('purple')
    expect(toneFor('detected')).toBe('blue')
  })

  it('desconocidos → neutral (sin inventar tonos)', () => {
    expect(toneFor('viral')).toBe('neutral')
    expect(toneFor('')).toBe('neutral')
  })
})

describe('toneClasses', () => {
  it('devuelve clases de Tailwind por tono', () => {
    expect(toneClasses('green')).toContain('text-emerald-400')
    expect(toneClasses('red')).toContain('text-red-400')
    expect(toneClasses('neutral')).toContain('text-neutral-300')
  })
})