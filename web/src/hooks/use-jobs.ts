import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { jobsApi, type JobListParams } from '@/lib/api/resources'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'

/** Lista paginada de jobs (por defecto el backend excluye `done`). Polling 10s. */
export function useJobsList(params?: JobListParams) {
  return useQuery({
    queryKey: ['jobs', params],
    queryFn: () => jobsApi.list(params),
    refetchInterval: REFRESH_DASHBOARD_MS,
  })
}

/** Conteos por tipo/estado (GET /api/jobs/stats). Polling 10s. */
export function useJobsStats() {
  return useQuery({
    queryKey: ['jobs', 'stats'],
    queryFn: jobsApi.stats,
    refetchInterval: REFRESH_DASHBOARD_MS,
  })
}

/** Re-encola un job en estado `error` (GET /api/jobs/:id/retry). */
export function useRetryJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => jobsApi.retry(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      void qc.invalidateQueries({ queryKey: ['system', 'overview'] })
    },
  })
}

/** Marca `error` un job `queued|running` (cancelación best-effort). */
export function useCancelJob() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => jobsApi.cancel(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      void qc.invalidateQueries({ queryKey: ['system', 'overview'] })
    },
  })
}