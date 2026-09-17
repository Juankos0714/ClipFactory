import { useQuery } from '@tanstack/react-query'
import { systemApi } from '@/lib/api/resources'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'

export function useHealth() {
  return useQuery({
    queryKey: ['health'],
    queryFn: systemApi.health,
    refetchInterval: REFRESH_DASHBOARD_MS,
  })
}

export function useSystemOverview() {
  return useQuery({
    queryKey: ['system', 'overview'],
    queryFn: systemApi.overview,
    refetchInterval: REFRESH_DASHBOARD_MS,
  })
}

export function useWorkers() {
  return useQuery({
    queryKey: ['workers'],
    queryFn: systemApi.workers,
    refetchInterval: REFRESH_DASHBOARD_MS,
  })
}