// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { renderHook, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { useCancelPublication, usePublicationsList, useRetryPublication } from './use-publications'
import { publicationsApi } from '@/lib/api/resources'
import type { Paginated, PublicationListItem } from '@/types/api'

vi.mock('@/lib/api/resources', () => ({
  publicationsApi: {
    list: vi.fn(),
    retry: vi.fn(),
    cancel: vi.fn(),
  },
}))

function wrapper({ children }: { children: React.ReactNode }) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
}

const emptyPage: Paginated<PublicationListItem> = {
  data: [],
  pagination: { page: 1, pageSize: 10, total: 0, pageCount: 0, hasNext: false, hasPrev: false },
}

describe('usePublicationsList', () => {
  beforeEach(() => vi.clearAllMocks())

  it('manda los filtros al backend tal cual', async () => {
    vi.mocked(publicationsApi.list).mockResolvedValueOnce(emptyPage)
    const params = { page: 1, pageSize: 10, platform: 'youtube', status: 'error' } as const
    const { result } = renderHook(() => usePublicationsList(params), { wrapper })

    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(publicationsApi.list).toHaveBeenCalledWith(params)
  })

  it('expone el error sin lanzar', async () => {
    vi.mocked(publicationsApi.list).mockRejectedValueOnce(new Error('network'))
    const { result } = renderHook(() => usePublicationsList(), { wrapper })

    await waitFor(() => expect(result.current.isError).toBe(true))
  })
})

describe('useRetryPublication', () => {
  beforeEach(() => vi.clearAllMocks())

  it('re-encola el publish de la publicación pedida', async () => {
    vi.mocked(publicationsApi.retry).mockResolvedValueOnce({ job: { id: 4, type: 'publish' } } as never)
    const { result } = renderHook(() => useRetryPublication(), { wrapper })

    result.current.mutate(3)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(publicationsApi.retry).toHaveBeenCalledWith(3)
  })

  it('expone el error sin lanzar para que el diálogo lo muestre', async () => {
    vi.mocked(publicationsApi.retry).mockRejectedValueOnce(new Error('ServerError'))
    const { result } = renderHook(() => useRetryPublication(), { wrapper })

    result.current.mutate(3)
    await waitFor(() => expect(result.current.isError).toBe(true))
  })
})

describe('useCancelPublication', () => {
  beforeEach(() => vi.clearAllMocks())

  it('marca la publicación pedida como dead-letter', async () => {
    vi.mocked(publicationsApi.cancel).mockResolvedValueOnce({ id: 3, status: 'failed' } as never)
    const { result } = renderHook(() => useCancelPublication(), { wrapper })

    result.current.mutate(3)
    await waitFor(() => expect(result.current.isSuccess).toBe(true))
    expect(publicationsApi.cancel).toHaveBeenCalledWith(3)
  })
})