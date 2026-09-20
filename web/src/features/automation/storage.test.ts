import { describe, expect, it } from 'vitest'
import { readDraftRules, writeDraftRules } from './storage'
import type { AutomationRule } from './types'

const RULE: AutomationRule = {
  id: 'abc-123',
  name: 'Descargar clips largos',
  enabled: true,
  trigger: 'clip_detected',
  conditions: { platform: 'twitch', source_id: 1, min_duration_seconds: 40 },
  action: 'download',
  createdAt: '2026-09-01T10:00:00Z',
  updatedAt: '2026-09-01T10:00:00Z',
}

describe('readDraftRules', () => {
  it('null/JSON roto → lista vacía (nunca rompe el arranque)', () => {
    expect(readDraftRules(null)).toEqual([])
    expect(readDraftRules('{not json')).toEqual([])
    expect(readDraftRules('{"a":1}')).toEqual([])
  })

  it('una entrada inválida descarta toda la lista (reformateo = key versionada)', () => {
    const raw = JSON.stringify([{ ...RULE, trigger: 'invented_event' }, RULE])
    expect(readDraftRules(raw)).toEqual([])
  })

  it('round-trip write → read devuelve la misma lista', () => {
    const rules = [RULE, { ...RULE, id: 'def', enabled: false }]
    expect(readDraftRules(writeDraftRules(rules))).toEqual(rules)
  })
})