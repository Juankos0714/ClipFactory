import { useState } from 'react'
import { ExternalLink, RefreshCw, XCircle } from 'lucide-react'
import { useCancelPublication, usePublicationsList, useRetryPublication } from '@/hooks/use-publications'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card } from '@/components/ui/card'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { Pagination } from '@/components/ui/pagination'
import { Select } from '@/components/ui/select'
import { StateView } from '@/components/ui/state'
import { formatDateTime } from '@/lib/format'
import { PUBLISHING_PLATFORM_LABELS, PUBLICATION_STATUS_OPTIONS, canCancel, canRetry } from './lib'
import type { PublicationListItem, PublicationPlatform, PublicationStatus } from '@/types/api'

const PAGE_SIZE = 20

/** Publicaciones por plataforma: filtros + tabla + retry/cancel (acciones reales). */
export function PublicationsPage() {
  const [page, setPage] = useState(1)
  const [platform, setPlatform] = useState<PublicationPlatform | ''>('')
  const [status, setStatus] = useState<PublicationStatus | ''>('')
  const [retryTarget, setRetryTarget] = useState<PublicationListItem | null>(null)
  const [cancelTarget, setCancelTarget] = useState<PublicationListItem | null>(null)

  const retryPublication = useRetryPublication()
  const cancelPublication = useCancelPublication()

  const { data, isLoading, error, refetch } = usePublicationsList(
    { page, pageSize: PAGE_SIZE, platform: platform || undefined, status: status || undefined },
    { refetchInterval: REFRESH_DASHBOARD_MS },
  )

  const publications = data?.data ?? []

  const confirmRetry = async () => {
    if (!retryTarget) return
    try {
      await retryPublication.mutateAsync(retryTarget.id)
      setRetryTarget(null)
    } catch {
      // El diálogo se queda abierto mostrando el error.
    }
  }
  const confirmCancel = async () => {
    if (!cancelTarget) return
    try {
      await cancelPublication.mutateAsync(cancelTarget.id)
      setCancelTarget(null)
    } catch {
      // El diálogo se queda abierto mostrando el error.
    }
  }

  const resetFilters = () => {
    setPage(1)
    setPlatform('')
    setStatus('')
  }

  return (
    <div className="flex flex-col gap-4">
      <Card className="p-4">
        <form
          className="grid grid-cols-1 gap-3 md:grid-cols-3"
          onSubmit={(e) => e.preventDefault()}
          aria-label="Filtros de publicaciones"
        >
          <Select
            label="Plataforma"
            name="pub-platform"
            placeholder="Todas"
            options={[
              { value: 'youtube', label: 'YouTube' },
              { value: 'meta', label: 'Meta' },
            ]}
            value={platform}
            onChange={(e) => {
              setPlatform(e.target.value as PublicationPlatform | '')
              setPage(1)
            }}
          />
          <Select
            label="Estado"
            name="pub-status"
            placeholder="Todos"
            options={PUBLICATION_STATUS_OPTIONS}
            value={status}
            onChange={(e) => {
              setStatus(e.target.value as PublicationStatus | '')
              setPage(1)
            }}
          />
          <div className="flex items-end">
            <button
              type="button"
              onClick={resetFilters}
              className="rounded-md px-2 py-1 text-xs font-medium text-neutral-400 hover:text-neutral-100"
            >
              Limpiar filtros
            </button>
          </div>
        </form>
      </Card>

      <Card>
        <StateView
          isLoading={isLoading}
          error={error}
          onRetry={() => void refetch()}
          isEmpty={publications.length === 0}
          emptyTitle="Sin publicaciones con estos filtros"
          emptyBody="Probá limpiar los filtros o creá una publicación desde el detalle de un clip."
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
            <table className="w-full min-w-[1000px] text-left text-sm">
              <thead>
                <tr className="border-b border-border text-xs uppercase tracking-wide text-neutral-500">
                  <th scope="col" className="px-3 py-3 font-medium">Clip</th>
                  <th scope="col" className="px-3 py-3 font-medium">Destino</th>
                  <th scope="col" className="px-3 py-3 font-medium">Estado</th>
                  <th scope="col" className="px-3 py-3 font-medium">Intentos</th>
                  <th scope="col" className="px-3 py-3 font-medium">Publicado</th>
                  <th scope="col" className="px-3 py-3 font-medium">Próximo reintento</th>
                  <th scope="col" className="px-3 py-3 font-medium">Error</th>
                  <th scope="col" className="px-3 py-3 text-right font-medium">Acciones</th>
                </tr>
              </thead>
              <tbody>
                {publications.map((pub) => {
                  const retryable = canRetry(pub.status)
                  const cancellable = canCancel(pub.status)
                  return (
                    <tr key={pub.id} className="border-b border-border/60 last:border-0">
                      <td className="max-w-[260px] px-3 py-3">
                        <p className="truncate font-medium text-neutral-100" title={pub.clip.title}>
                          {pub.clip.title}
                        </p>
                        <p className="text-xs text-neutral-500">#{pub.clip_id} · {pub.clip.platform}</p>
                      </td>
                      <td className="px-3 py-3">
                        <Badge tone={pub.platform === 'youtube' ? 'red' : 'blue'}>{PUBLISHING_PLATFORM_LABELS[pub.platform]}</Badge>
                      </td>
                      <td className="px-3 py-3">
                        <StatusBadge status={pub.status} />
                      </td>
                      <td className="px-3 py-3 tabular-nums text-neutral-400">{pub.attempts}</td>
                      <td className="px-3 py-3 text-neutral-400">{formatDateTime(pub.published_at)}</td>
                      <td className="px-3 py-3 text-neutral-400">{formatDateTime(pub.next_retry_at)}</td>
                      <td className="max-w-[220px] px-3 py-3 text-xs text-red-400">
                        {pub.error_message ? (
                          <span className="truncate" title={pub.error_message}>
                            {pub.error_message}
                          </span>
                        ) : (
                          <span className="text-neutral-600">—</span>
                        )}
                      </td>
                      <td className="px-3 py-3">
                        <div className="flex flex-wrap items-center justify-end gap-1">
                          {pub.external_url ? (
                            <a
                              href={pub.external_url}
                              target="_blank"
                              rel="noreferrer"
                              className="inline-flex items-center gap-1 rounded-md px-2.5 py-1.5 text-xs font-medium text-brand hover:underline"
                            >
                              <ExternalLink aria-hidden="true" className="h-4 w-4" />
                              Ver
                            </a>
                          ) : null}
                          {retryable ? (
                            <Button
                              size="sm"
                              variant="danger"
                              loading={retryPublication.isPending && retryTarget?.id === pub.id}
                              onClick={() => setRetryTarget(pub)}
                            >
                              <RefreshCw aria-hidden="true" className="h-4 w-4" />
                              Reintentar
                            </Button>
                          ) : null}
                          {cancellable ? (
                            <Button
                              size="sm"
                              variant="ghost"
                              loading={cancelPublication.isPending && cancelTarget?.id === pub.id}
                              onClick={() => setCancelTarget(pub)}
                            >
                              <XCircle aria-hidden="true" className="h-4 w-4" />
                              Cancelar
                            </Button>
                          ) : null}
                          {!retryable && !cancellable && !pub.external_url ? (
                            <span className="text-neutral-600">—</span>
                          ) : null}
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

      <ConfirmDialog
        open={retryTarget != null}
        onClose={() => setRetryTarget(null)}
        onConfirm={() => void confirmRetry()}
        title="Reintentar publicación"
        message={
          retryTarget
            ? retryPublication.error
              ? `${retryPublication.error.message} Probá más tarde o revisá los logs.`
              : `Se re-encolará el job de publicación de "${retryTarget.clip.title}" en ${PUBLISHING_PLATFORM_LABELS[retryTarget.platform]}.`
            : ''
        }
        confirmLabel="Reintentar"
        loading={retryPublication.isPending}
      />
      <ConfirmDialog
        open={cancelTarget != null}
        onClose={() => setCancelTarget(null)}
        onConfirm={() => void confirmCancel()}
        title="Cancelar publicación"
        message={
          cancelTarget
            ? cancelPublication.error
              ? `${cancelPublication.error.message} La publicación puede quedar sin cambios.`
              : `"${cancelTarget.clip.title}" (${PUBLISHING_PLATFORM_LABELS[cancelTarget.platform]}) se marcará como fallida (dead-letter) y no se volverá a intentar automáticamente.`
            : ''
        }
        confirmLabel="Cancelar"
        loading={cancelPublication.isPending}
      />
    </div>
  )
}