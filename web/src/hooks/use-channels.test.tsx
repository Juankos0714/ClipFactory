// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useSources, useCreateSource, useTriggerDiscovery } from './use-channels'
import { sourcesApi } from '@/lib/api/resources'
import type { Paginated, Source } from '@/types/api'

vi.mock('@/lib/api/resources', () => ({
  sourcesApi: {
    list: vi.fn(),
    create: vi.fn(),
    patch: vi.fn(),
    remove: vi.fn(),
    discover: vi.fn(),
  },
}))

function wrapper({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

const emptyPage: Paginated<Source> = {
  data: [],
  pagination: { page: 1, pageSize: 50, total: 0, pageCount: 0, hasNext: false, hasPrev: false },
}

describe('useSources', () => {
  beforeEach(() => vi.clearAllMocks())

  it('carga la lista de sources con el envelope paginado', async () => {
    vi.mocked(sourcesApi.list).mockResolvedValueOnce(emptyPage)
    const { result } = renderHook(() => useSources(), { wrapper })
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(sourcesApi.list).toHaveBeenCalledWith(undefined)
    expect(result.current.data).toEqual(emptyPage)
  })

  it('expone el estado de error sin lanzar', async () => {
    vi.mocked(sourcesApi.list).mockRejectedValueOnce(new Error('network'))
    const { result } = renderHook(() => useSources(), { wrapper })
    await waitFor(() => expect(result.current.isError).toBe(true))
  })
})

describe('useCreateSource', () => {
  beforeEach(() => vi.clearAllMocks())

  it('manda el input al backend y expone isSuccess', async () => {
    const created = { id: 7, platform: 'twitch', channel_id: '1', channel_name: 'x', active: true }
    vi.mocked(sourcesApi.create).mockResolvedValueOnce(created as never)
    const input = { platform: 'twitch' as const, channel_id: '1', channel_name: 'x', active: true }
    const { result } = renderHook(() => useCreateSource(), { wrapper })
    result.current.mutate(input)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(sourcesApi.create).toHaveBeenCalledWith(input)
  })
})

describe('useTriggerDiscovery', () => {
  beforeEach(() => vi.clearAllMocks())

  it('encola discovery para el source pedido', async () => {
    vi.mocked(sourcesApi.discover).mockResolvedValueOnce({ job: { id: 3, type: 'discovery' } } as never)
    const { result } = renderHook(() => useTriggerDiscovery(), { wrapper })
    result.current.mutate(7)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(sourcesApi.discover).toHaveBeenCalledWith(7)
  })
})