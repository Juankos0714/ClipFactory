import { useState } from 'react'
import { Eye, Image as ImageIcon, RotateCcw } from 'lucide-react'
import { useClipsList, useQueueForProcess, useRegenerateThumbnail } from '@/hooks/use-clips'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { Pagination } from '@/components/ui/pagination'
import { StateView } from '@/components/ui/state'
import { formatDateTime, formatDuration } from '@/lib/format'
import type { ClipListItem } from '@/types/api'
import { PipelineSummary } from './components/pipeline-summary'
import { ProductionFilters, type ProductionFiltersState } from './components/production-filters'
import { PublicationsCard } from './components/publications-card'
import { ClipPreviewDialog } from './components/clip-preview-dialog'

const PAGE_SIZE = 20

/** Pipeline operativo: contenido por etapa + tabla accionable + publicaciones. */
export function ProductionPage() {
  const [page, setPage] = useState(1)
  const [filters, setFilters] = useState<ProductionFiltersState>({})
  const [preview, setPreview] = useState<ClipListItem | null>(null)

  const queueForProcess = useQueueForProcess()
  const regenerateThumbnail = useRegenerateThumbnail()

  const { data, isLoading, error, refetch } = useClipsList(
    { page, pageSize: PAGE_SIZE, platform: filters.platform, status: filters.status, sort: 'newest' },
    { refetchInterval: REFRESH_DASHBOARD_MS },
  )

  const clips = data?.data ?? []
  const busy = queueForProcess.isPending || regenerateThumbnail.isPending
  const actionError = queueForProcess.error ?? regenerateThumbnail.error

  const patchFilters = (patch: Partial<ProductionFiltersState>) => {
    setPage(1)
    setFilters((prev) => ({ ...prev, ...patch }))
  }
  const resetFilters = () => patchFilters({ platform: undefined, status: undefined })

  const canPreview = (clip: ClipListItem) => clip.video != null || clip.clip?.status === 'completed'

  return (
    <div className="flex flex-col gap-4">
      <PipelineSummary />

      <Card className="p-4">
        <ProductionFilters value={filters} onChange={patchFilters} onReset={resetFilters} />
      </Card>

      {actionError ? (
        <p role="alert" className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400">
          {actionError.message}
        </p>
      ) : null}

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
              onClick={resetFilters}
              className="rounded-md px-2 py-1 text-xs font-medium text-brand hover:underline"
            >
              Limpiar filtros
            </button>
          }
          loadingRows={8}
        >
          <div className="overflow-x-auto">
            <table className="w-full min-w-[860px] text-left text-sm">
              <thead>
                <tr className="border-b border-border text-xs uppercase tracking-wide text-neutral-500">
                  <th scope="col" className="px-3 py-3 font-medium">Clip</th>
                  <th scope="col" className="px-3 py-3 font-medium">Canal</th>
                  <th scope="col" className="px-3 py-3 font-medium">Duración</th>
                  <th scope="col" className="px-3 py-3 font-medium">Estado</th>
                  <th scope="col" className="px-3 py-3 font-medium">Detectado</th>
                  <th scope="col" className="px-3 py-3 text-right font-medium">Acciones</th>
                </tr>
              </thead>
              <tbody>
                {clips.map((clip) => {
                  const previewable = canPreview(clip)
                  const reprocessable = clip.clip != null
                  const thumbnailable = clip.clip?.thumbnail_path != null
                  return (
                    <tr key={clip.source_clip.id} className="border-b border-border/60 last:border-0">
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
                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center justify-end gap-1">
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={!previewable || busy}
                            onClick={() => setPreview(clip)}
                            title={previewable ? 'Vista previa del video' : 'Sin archivo de video disponible'}
                          >
                            <Eye aria-hidden="true" className="h-4 w-4" />
                            Ver
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={!reprocessable || busy}
                            onClick={() => queueForProcess.mutate(clip.source_clip.id)}
                            title={reprocessable ? 'Re-encolar procesado' : 'El clip aún no fue procesado'}
                          >
                            <RotateCcw aria-hidden="true" className="h-4 w-4" />
                            Reprocesar
                          </Button>
                          <Button
                            size="sm"
                            variant="ghost"
                            disabled={!thumbnailable || busy}
                            onClick={() => regenerateThumbnail.mutate(clip.source_clip.id)}
                            title={thumbnailable ? 'Regenerar miniatura' : 'Sin miniatura generada'}
                          >
                            <ImageIcon aria-hidden="true" className="h-4 w-4" />
                            Miniatura
                          </Button>
                        </div>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
          {data ? <Pagination meta={data.pagination} onChange={(p) => setPage(p)} /> : null}
        </StateView>
      </Card>

      <PublicationsCard />

      <ClipPreviewDialog clip={preview} onClose={() => setPreview(null)} />
    </div>
  )
}