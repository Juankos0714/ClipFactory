// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useHealth, useSystemOverview, useWorkers } from './use-system'
import { systemApi } from '@/lib/api/resources'
import type { Health, SystemOverview, WorkerInfo } from '@/types/api'

vi.mock('@/lib/api/resources', () => ({
  systemApi: {
    health: vi.fn(),
    overview: vi.fn(),
    workers: vi.fn(),
  },
}))

function wrapper({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

const overview: SystemOverview = {
  sources: { twitch: { active: 1, inactive: 0 } },
  source_clips: {
    twitch: { detected: 2, downloaded: 1, skipped: 0, error: 0 },
  },
  videos: { incoming: 0, processing: 0, completed: 1, failed: 0 },
  clips: { processing: 0, completed: 1, failed: 0 },
  publications: { youtube: { pending: 0, published: 1, error: 0, waiting_rate_limit: 0, failed: 0 } },
  jobs: { discovery: { queued: 0, running: 1, done: 0, error: 0 } },
}

const health: Health = { status: 'ok', schema_version: 2, worker: 'running' }

describe('useHealth', () => {
  beforeEach(() => vi.clearAllMocks())

  it('expone el health del backend', async () => {
    vi.mocked(systemApi.health).mockResolvedValueOnce(health)
    const { result } = renderHook(() => useHealth(), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual(health)
  })

  it('expone el error sin lanzar', async () => {
    vi.mocked(systemApi.health).mockRejectedValueOnce(new Error('network'))
    const { result } = renderHook(() => useHealth(), { wrapper })

    await waitFor(() => expect(result.current.isError).toBe(true))
  })
})

describe('useSystemOverview', () => {
  beforeEach(() => vi.clearAllMocks())

  it('trae los conteos agregados del backend', async () => {
    vi.mocked(systemApi.overview).mockResolvedValueOnce(overview)
    const { result } = renderHook(() => useSystemOverview(), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data?.clips.completed).toBe(1)
  })
})

describe('useWorkers', () => {
  beforeEach(() => vi.clearAllMocks())

  it('devuelve la lista de workers desenvuelta del envelope', async () => {
    const workers: WorkerInfo[] = [{ lockedBy: 'worker-1', jobType: 'discovery', lastLockedAt: null }]
    vi.mocked(systemApi.workers).mockResolvedValueOnce(workers)
    const { result } = renderHook(() => useWorkers(), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(result.current.data).toEqual(workers)
  })
})