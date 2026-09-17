import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { clipsApi } from '@/lib/api/resources'
import type { ClipListParams } from '@/types/api'

export function useClipsList(params?: ClipListParams, opts?: { refetchInterval?: number }) {
  return useQuery({
    queryKey: ['clips', params],
    queryFn: () => clipsApi.list(params),
    ...opts,
  })
}

export function useClipDetail(id: number | null) {
  return useQuery({
    queryKey: ['clips', id, 'detail'],
    queryFn: () => clipsApi.detail(id as number),
    enabled: id != null,
  })
}

export function useQueueForDownload() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (sourceClipId: number) => clipsApi.download(sourceClipId),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['clips'] }),
  })
}

/** Encola un job `process` para re-procesar un clip ya procesado. */
export function useQueueForProcess() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (clipId: number) => clipsApi.queueForProcess(clipId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['clips'] })
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      void qc.invalidateQueries({ queryKey: ['system', 'overview'] })
    },
  })
}

/** Encola un job `thumbnail` para regenerar la miniatura de un clip. */
export function useRegenerateThumbnail() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (clipId: number) => clipsApi.regenerateThumbnail(clipId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['clips'] })
      void qc.invalidateQueries({ queryKey: ['jobs'] })
      void qc.invalidateQueries({ queryKey: ['system', 'overview'] })
    },
  })
}