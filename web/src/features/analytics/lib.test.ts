import { describe, expect, it } from 'vitest'
import { jobsStack, publishedPerDay, publicationStack } from './lib'
import type { PublicationListItem, SystemOverview } from '@/types/api'

const OVERVIEW: SystemOverview = {
  sources: {},
  source_clips: {},
  videos: { incoming: 0, processing: 0, completed: 0, failed: 0 },
  clips: { processing: 0, completed: 0, failed: 0 },
  publications: {
    youtube: { pending: 1, published: 2, error: 1, waiting_rate_limit: 0, failed: 0 },
    meta: { pending: 0, published: 0, error: 0, waiting_rate_limit: 1, failed: 1 },
  },
  jobs: {
    discovery: { queued: 1, running: 0, done: 42, error: 0 },
    publish: { queued: 2, running: 1, done: 10, error: 1 },
  },
}

describe('publicationStack', () => {
  it('pivotea plataforma × estado con ceros explícitos', () => {
    const [yt, meta] = publicationStack(OVERVIEW)
    expect(yt).toEqual({ platform: 'youtube', pending: 1, published: 2, error: 1, waiting_rate_limit: 0, failed: 0 })
    expect(meta).toEqual({ platform: 'meta', pending: 0, published: 0, error: 0, waiting_rate_limit: 1, failed: 1 })
  })
})

describe('jobsStack', () => {
  it('pivotea tipo × estado', () => {
    expect(jobsStack(OVERVIEW)).toEqual([
      { type: 'discovery', queued: 1, running: 0, done: 42, error: 0 },
      { type: 'publish', queued: 2, running: 1, done: 10, error: 1 },
    ])
  })
})

describe('publishedPerDay', () => {
  const NOW = new Date('2026-09-18T12:00:00Z')
  const pub = (published_at: string): PublicationListItem =>
    ({
      id: 1,
      clip_id: 1,
      platform: 'youtube',
      status: 'published',
      attempts: 0,
      next_retry_at: null,
      external_id: 'x',
      external_url: 'https://example.com',
      error_message: null,
      published_at,
      created_at: published_at,
      updated_at: published_at,
      clip: { id: 1, title: 't', platform: 'twitch' },
    })

  it('rellena los 30 días con ceros y suma por día UTC', () => {
    const series = publishedPerDay([pub('2026-09-18T07:00:00Z'), pub('2026-09-18T20:00:00Z'), pub('2026-09-10T00:00:00Z')], 30, NOW)
    expect(series).toHaveLength(30)
    expect(series[29]).toEqual({ day: '2026-09-18', published: 2 })
    expect(series.find((s) => s.day === '2026-09-10')).toEqual({ day: '2026-09-10', published: 1 })
    expect(series.find((s) => s.day === '2026-09-11')).toEqual({ day: '2026-09-11', published: 0 })
  })

  it('ignora publicaciones sin published_at o inválidas', () => {
    const series = publishedPerDay([pub('2026-09-18T07:00:00Z'), { ...pub('x'), published_at: 'no-date' }], 2, NOW)
    expect(series.reduce((sum, s) => sum + s.published, 0)).toBe(1)
  })
})