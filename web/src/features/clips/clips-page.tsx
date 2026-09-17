import { useMemo, useState } from 'react'
import { Link } from 'react-router-dom'
import { useClipsList, useQueueForDownload } from '@/hooks/use-clips'
import { useSources } from '@/hooks/use-channels'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { StateView } from '@/components/ui/state'
import { Pagination } from '@/components/ui/pagination'
import { formatDateTime, formatDuration } from '@/lib/format'
import { ClipFilters, type ClipFiltersState } from './components/clip-filters'
import type { ClipStatusFilter } from '@/types/api'

const PAGE_SIZE = 20

export function ClipsPage() {
  const [page, setPage] = useState(1)
  const [filters, setFilters] = useState<ClipFiltersState>({})
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const queueForDownload = useQueueForDownload()

  const { data: sources } = useSources()
  const channelOptions = useMemo(
    () =>
      (sources?.data ?? []).map((s) => ({
        value: String(s.id),
        label: s.channel_name,
      })),
    [sources],
  )

  const { data, isLoading, error, refetch } = useClipsList({
    page,
    pageSize: PAGE_SIZE,
    channel_id: filters.channelId ? Number(filters.channelId) : undefined,
    platform: filters.platform as 'twitch' | 'kick' | undefined,
    status: filters.status as ClipStatusFilter | undefined,
    sort: (filters.sort ?? 'newest') as 'newest' | 'duration',
  })

  const clips = data?.data ?? []
  const selectedList = clips.filter((c) => selected.has(c.source_clip.id))
  const allSelectable = selectedList.length > 0 && selectedList.every((c) => c.source_clip.status === 'detected')
  const allOnPageChecked = clips.length > 0 && clips.every((c) => selected.has(c.source_clip.id))

  const patchFilters = (patch: Partial<ClipFiltersState>) => {
    setPage(1)
    setSelected(new Set())
    setFilters((prev) => ({ ...prev, ...patch }))
  }

  const toggle = (id: number) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  const togglePage = () => {
    setSelected(allOnPageChecked ? new Set() : new Set(clips.map((c) => c.source_clip.id)))
  }

  const enqueueSelected = async () => {
    await Promise.all(selectedList.map((c) => queueForDownload.mutateAsync(c.source_clip.id)))
    setSelected(new Set())
  }

  return (
    <div className="flex flex-col gap-4">
      <Card className="p-4">
        <ClipFilters
          value={filters}
          channels={channelOptions}
          onChange={patchFilters}
          onReset={() => patchFilters({})}
        />
      </Card>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <p role="status" className="text-sm text-neutral-400">
          {selected.size > 0 ? `${selected.size} seleccionado(s)` : 'Clips detectados por el pipeline'}
        </p>
        <Button
          variant="secondary"
          size="sm"
          disabled={!allSelectable}
          loading={queueForDownload.isPending}
          onClick={() => void enqueueSelected()}
          title={allSelectable ? 'Encolar descarga de los clips seleccionados' : 'Solo los clips detectados son descargables'}
        >
          Encolar descarga ({selected.size})
        </Button>
      </div>

      <Card>
        <StateView
          isLoading={isLoading}
          error={error}
          onRetry={() => void refetch()}
          isEmpty={clips.length === 0}
          emptyTitle="No hay clips con estos filtros"
          emptyBody="Probá limpiar los filtros o esperá a que el discovery detecte nuevo contenido."
          emptyAction={
            <button
              type="button"
              onClick={() => patchFilters({})}
              className="rounded-md px-2 py-1 text-xs font-medium text-brand hover:underline"
            >
              Limpiar filtros
            </button>
          }
          loadingRows={8}
        >
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-left text-sm">
              <thead>
                <tr className="border-b border-border text-xs uppercase tracking-wide text-neutral-500">
                  <th scope="col" className="w-10 px-3 py-3">
                    <span className="sr-only">Seleccionar página</span>
                    <input
                      type="checkbox"
                      aria-label="Seleccionar todos los de la página"
                      checked={allOnPageChecked}
                      onChange={togglePage}
                      className="h-4 w-4 accent-brand"
                    />
                  </th>
                  <th scope="col" className="px-3 py-3 font-medium">Clip</th>
                  <th scope="col" className="px-3 py-3 font-medium">Canal</th>
                  <th scope="col" className="px-3 py-3 font-medium">Duración</th>
                  <th scope="col" className="px-3 py-3 font-medium">Estado</th>
                  <th scope="col" className="px-3 py-3 font-medium">Detectado</th>
                  <th scope="col" className="px-3 py-3 text-right font-medium">Ver</th>
                </tr>
              </thead>
              <tbody>
                {clips.map((clip) => {
                  const checked = selected.has(clip.source_clip.id)
                  return (
                    <tr key={clip.source_clip.id} className="border-b border-border/60 last:border-0">
                      <td className="px-3 py-3">
                        <input
                          type="checkbox"
                          aria-label={`Seleccionar ${clip.source_clip.title}`}
                          checked={checked}
                          onChange={() => toggle(clip.source_clip.id)}
                          className="h-4 w-4 accent-brand"
                        />
                      </td>
                      <td className="max-w-[280px] px-3 py-3">
                        <p className="truncate font-medium text-neutral-100" title={clip.source_clip.title}>
                          {clip.source_clip.title}
                        </p>
                        <p className="text-xs text-neutral-500">
                          {clip.source_clip.platform} · {clip.source_clip.platform_clip_id}
                        </p>
                      </td>
                      <td className="px-3 py-3 text-neutral-400">{clip.channel.channel_name}</td>
                      <td className="px-3 py-3 tabular-nums text-neutral-400">
                        {formatDuration(clip.source_clip.duration_seconds)}
                      </td>
                      <td className="px-3 py-3">
                        <StatusBadge status={clip.source_clip.status} />
                      </td>
                      <td className="px-3 py-3 text-neutral-400">
                        {formatDateTime(clip.source_clip.created_at_platform)}
                      </td>
                      <td className="px-3 py-3 text-right">
                        <Link
                          to={`/clips/${clip.source_clip.id}`}
                          className="text-sm font-medium text-brand hover:underline"
                        >
                          Ver
                        </Link>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          {data ? (
            <Pagination meta={data.pagination} onChange={(p) => setPage(p)} />
          ) : null}
        </StateView>
      </Card>
    </div>
  )
}