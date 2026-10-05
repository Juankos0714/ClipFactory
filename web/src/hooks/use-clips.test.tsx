// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useClipDetail, useClipsList, useQueueForProcess, useRegenerateThumbnail } from './use-clips'
import { clipsApi } from '@/lib/api/resources'
import type { ClipListItem, Paginated } from '@/types/api'

vi.mock('@/lib/api/resources', () => ({
  clipsApi: {
    list: vi.fn(),
    detail: vi.fn(),
    download: vi.fn(),
    queueForProcess: vi.fn(),
    regenerateThumbnail: vi.fn(),
  },
}))

function wrapper({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

const emptyPage: Paginated<ClipListItem> = {
  data: [],
  pagination: { page: 1, pageSize: 20, total: 0, pageCount: 0, hasNext: false, hasPrev: false },
}

describe('useClipsList', () => {
  beforeEach(() => vi.clearAllMocks())

  it('manda los filtros al backend tal cual', async () => {
    vi.mocked(clipsApi.list).mockResolvedValueOnce(emptyPage)
    const params = { page: 1, pageSize: 20, platform: 'kick', status: 'downloaded', sort: 'newest' } as const
    const { result } = renderHook(() => useClipsList(params), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(clipsApi.list).toHaveBeenCalledWith(params)
  })

  it('expone el error sin lanzar', async () => {
    vi.mocked(clipsApi.list).mockRejectedValueOnce(new Error('network'))
    const { result } = renderHook(() => useClipsList(), { wrapper })

    await waitFor(() => expect(result.current.isError).toBe(true))
  })
})

describe('useClipDetail', () => {
  beforeEach(() => vi.clearAllMocks())

  it('no pide el detalle mientras el id es null', () => {
    const { result } = renderHook(() => useClipDetail(null), { wrapper })

    expect(result.current.fetchStatus).toBe('idle')
    expect(clipsApi.detail).not.toHaveBeenCalled()
  })

  it('pide el detalle cuando recibe un id', async () => {
    vi.mocked(clipsApi.detail).mockResolvedValueOnce({ source_clip: { id: 2 } } as never)
    const { result } = renderHook(() => useClipDetail(2), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(clipsApi.detail).toHaveBeenCalledWith(2)
  })

  it('expone el error sin lanzar', async () => {
    vi.mocked(clipsApi.detail).mockRejectedValueOnce(new Error('NotFoundError'))
    const { result } = renderHook(() => useClipDetail(999), { wrapper })

    await waitFor(() => expect(result.current.isError).toBe(true))
  })
})

describe('acciones que encolan jobs', () => {
  beforeEach(() => vi.clearAllMocks())

  it('useQueueForProcess encola el procesado del clip pedido', async () => {
    vi.mocked(clipsApi.queueForProcess).mockResolvedValueOnce({ job: { id: 9, type: 'process' } } as never)
    const { result } = renderHook(() => useQueueForProcess(), { wrapper })

    result.current.mutate(2)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(clipsApi.queueForProcess).toHaveBeenCalledWith(2)
  })

  it('useRegenerateThumbnail encola la miniatura del clip pedido', async () => {
    vi.mocked(clipsApi.regenerateThumbnail).mockResolvedValueOnce({ job: { id: 10, type: 'thumbnail' } } as never)
    const { result } = renderHook(() => useRegenerateThumbnail(), { wrapper })

    result.current.mutate(2)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(clipsApi.regenerateThumbnail).toHaveBeenCalledWith(2)
  })

  it('expone el error de la acción sin lanzar', async () => {
    vi.mocked(clipsApi.queueForProcess).mockRejectedValueOnce(new Error('ServerError'))
    const { result } = renderHook(() => useQueueForProcess(), { wrapper })

    result.current.mutate(2)
    await waitFor(() => expect(result.current.isError).toBe(true))
  })
})