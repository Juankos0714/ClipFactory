import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { sourcesApi, type SourceListParams } from '@/lib/api/resources'
import type { SourceCreateInput, SourceUpdateInput } from '@/types/api'

export function useSources(params?: SourceListParams) {
  return useQuery({
    queryKey: ['sources', params],
    queryFn: () => sourcesApi.list(params),
  })
}

export function useCreateSource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (input: SourceCreateInput) => sourcesApi.create(input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sources'] }),
  })
}

export function useUpdateSource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, input }: { id: number; input: SourceUpdateInput }) =>
      sourcesApi.patch(id, input),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sources'] }),
  })
}

export function useDeleteSource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => sourcesApi.remove(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['sources'] }),
  })
}

export function useTriggerDiscovery() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => sourcesApi.discover(id),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['sources'] })
      void qc.invalidateQueries({ queryKey: ['jobs'] })
    },
  })
}