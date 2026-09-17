import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { publicationsApi, type PublicationListParams } from '@/lib/api/resources'

/** Lista paginada de publicaciones (GET /api/publications). */
export function usePublicationsList(params?: PublicationListParams, opts?: { refetchInterval?: number }) {
  return useQuery({
    queryKey: ['publications', params],
    queryFn: () => publicationsApi.list(params),
    ...opts,
  })
}

/** Re-encola el job `publish` de una publicación en estado `error`. */
export function useRetryPublication() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => publicationsApi.retry(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['publications'] })
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      void qc.invalidateQueries({ queryKey: ['system', 'overview'] })
    },
  })
}