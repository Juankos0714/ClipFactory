import { describe, expect, it } from 'vitest'
import { canCancel, canRetry } from './lib'
import type { PublicationStatus } from '@/types/api'

const ALL: PublicationStatus[] = ['pending', 'published', 'error', 'waiting_rate_limit', 'failed']

describe('canRetry', () => {
  it('solo re-encola el estado error', () => {
    for (const s of ALL) expect(canRetry(s), s).toBe(s === 'error')
  })
})

describe('canCancel', () => {
  it('dead-letter solo antes de publicarse', () => {
    for (const s of ALL) {
      expect(canCancel(s), s).toBe(s === 'pending' || s === 'error' || s === 'waiting_rate_limit')
    }
  })
})