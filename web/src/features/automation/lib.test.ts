import { describe, expect, it } from 'vitest'
import { describeConditions, describeRule } from './lib'
import type { AutomationConditions } from './types'

const EMPTY: AutomationConditions = { platform: '', source_id: null, min_duration_seconds: null }

describe('describeConditions', () => {
  it('sin condiciones → lista vacía', () => {
    expect(describeConditions(EMPTY)).toEqual([])
  })

  it('plataforma → "en Twitch"/"en Kick"', () => {
    expect(describeConditions({ ...EMPTY, platform: 'twitch' })).toEqual(['en Twitch'])
    expect(describeConditions({ ...EMPTY, platform: 'kick' })).toEqual(['en Kick'])
  })

  it('source_id y duración mínima', () => {
    expect(describeConditions({ ...EMPTY, source_id: 2 })).toEqual(['del canal #2'])
    expect(describeConditions({ ...EMPTY, min_duration_seconds: 40 })).toEqual(['de al menos 40 s'])
  })

  it('condiciones combinadas', () => {
    expect(describeConditions({ platform: 'kick', source_id: 7, min_duration_seconds: 60 })).toEqual([
      'en Kick',
      'del canal #7',
      'de al menos 60 s',
    ])
  })
})

describe('describeRule', () => {
  it('CUÁNDO → ENTONCES sin condiciones', () => {
    expect(describeRule({ trigger: 'clip_detected', conditions: EMPTY, action: 'download' })).toBe(
      'Cuando se detecta un clip → Descargar el clip',
    )
  })

  it('incluye las condiciones entre paréntesis', () => {
    const rule = describeRule({
      trigger: 'clip_processed',
      conditions: { platform: 'twitch', source_id: 3, min_duration_seconds: 45 },
      action: 'publish_youtube',
    })
    expect(rule).toContain('Cuando el clip se procesa')
    expect(rule).toContain('en Twitch')
    expect(rule).toContain('del canal #3')
    expect(rule).toContain('de al menos 45 s')
    expect(rule).toContain('→ Publicar en YouTube')
  })
})