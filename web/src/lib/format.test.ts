import { describe, expect, it } from 'vitest'
import { formatDateTime, formatDuration, formatNumber } from './format'

describe('formatDateTime', () => {
  it('emu dash para null, vacío e inválidos', () => {
    expect(formatDateTime(null)).toBe('—')
    expect(formatDateTime(undefined)).toBe('—')
    expect(formatDateTime('')).toBe('—')
    expect(formatDateTime('not-a-date')).toBe('—')
  })

  it('formatea RFC3339 UTC en es', () => {
    const out = formatDateTime('2024-01-02T03:04:05Z')
    expect(out).not.toBe('—')
    expect(out).toContain('3:04')
    expect(out).toContain('24')
    expect(out.length).toBeGreaterThan(5)
  })
})

describe('formatDuration', () => {
  it('emu dash para valores nulos/NaN', () => {
    expect(formatDuration(null)).toBe('—')
    expect(formatDuration(Number.NaN)).toBe('—')
  })

  it('mm:ss', () => {
    expect(formatDuration(0)).toBe('00:00')
    expect(formatDuration(90)).toBe('01:30')
    expect(formatDuration(61)).toBe('01:01')
  })

  it('h:mm:ss a partir de 1 hora', () => {
    expect(formatDuration(3600)).toBe('1:00:00')
    expect(formatDuration(3661)).toBe('1:01:01')
  })
})

describe('formatNumber', () => {
  it('emu dash para valores nulos/NaN', () => {
    expect(formatNumber(null)).toBe('—')
    expect(formatNumber(undefined)).toBe('—')
    expect(formatNumber(Number.NaN)).toBe('—')
  })

  it('notación compacta', () => {
    expect(formatNumber(0)).toBe('0')
    expect(formatNumber(1_234)).not.toBe('—')
    expect(formatNumber(100_000)).not.toBe('—')
  })
})