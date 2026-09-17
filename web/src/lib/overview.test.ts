import { describe, expect, it } from 'vitest'
import { activeSources, jobsBy, publicationsBy, sourceClipsBy } from './overview'
import type { SystemOverview } from '@/types/api'

const overview = {
  sources: { twitch: { active: 2, inactive: 1 } },
  source_clips: {
    twitch: { detected: 5, downloaded: 3, skipped: 1, error: 1 },
  },
  videos: { incoming: 0, processing: 0, completed: 2, failed: 0 },
  clips: { processing: 0, completed: 2, failed: 0 },
  publications: { youtube: { pending: 1, published: 2, error: 0, waiting_rate_limit: 1, failed: 0 } },
  jobs: { discovery: { queued: 1, running: 1, done: 10, error: 1 } },
} satisfies SystemOverview

describe('overview helpers', () => {
  it('suma canales activos entre plataformas', () => {
    expect(activeSources(overview)).toBe(2)
  })

  it('suma source_clips de un estado dado entre plataformas', () => {
    expect(sourceClipsBy(overview, 'detected')).toBe(5)
    expect(sourceClipsBy(overview, 'skipped')).toBe(1)
  })

  it('suma publicaciones por estado entre plataformas', () => {
    expect(publicationsBy(overview, 'waiting_rate_limit')).toBe(1)
    expect(publicationsBy(overview, 'published')).toBe(2)
  })

  it('suma jobs por estado entre tipos', () => {
    expect(jobsBy(overview, 'error')).toBe(1)
    expect(jobsBy(overview, 'done')).toBe(10)
  })
})