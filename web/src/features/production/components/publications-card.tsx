import { useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { usePublicationsList, useRetryPublication } from '@/hooks/use-publications'
import { REFRESH_DASHBOARD_MS } from '@/lib/config/env'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { ConfirmDialog } from '@/components/ui/confirm-dialog'
import { StateView } from '@/components/ui/state'
import { formatDateTime } from '@/lib/format'
import type { PublicationListItem } from '@/types/api'

/** Últimas publicaciones + reintento de publicaciones en estado `error`. */
export function PublicationsCard() {
  const [retryTarget, setRetryTarget] = useState<PublicationListItem | null>(null)
  const retryPublication = useRetryPublication()

  const { data, isLoading, error, refetch } = usePublicationsList(
    { page: 1, pageSize: 10 },
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

  return (
    <Card>
      <CardHeader id="publications" title="Publicaciones recientes" />
      <StateView
        isLoading={isLoading}
        error={error}
        onRetry={() => void refetch()}
        isEmpty={publications.length === 0}
        emptyTitle="Sin publicaciones todavía"
        emptyBody="Cuando los clips se publiquen, aparecerán acá."
        loadingRows={4}
      >
        {data ? (
          <ul className="divide-y divide-border">
            {publications.map((pub) => (
              <li
                key={pub.id}
                className="flex flex-col gap-1 px-4 py-2.5 sm:flex-row sm:items-center sm:justify-between sm:gap-3"
              >
                <div className="min-w-0">
                  <p className="truncate text-sm font-medium text-neutral-200" title={pub.clip.title}>
                    {pub.clip.title}
                  </p>
                  <p className="text-xs text-neutral-500">
                    {pub.platform}
                    {pub.published_at ? <> · {formatDateTime(pub.published_at)}</> : null}
                    {pub.external_url ? (
                      <>
                        {' '}
                        ·{' '}
                        <a href={pub.external_url} target="_blank" rel="noreferrer" className="text-brand hover:underline">
                          ver en la plataforma ↗
                        </a>
                      </>
                    ) : null}
                  </p>
                  {pub.error_message ? <p className="text-xs text-red-400">{pub.error_message}</p> : null}
                </div>
                <div className="flex items-center gap-2">
                  {pub.status === 'error' ? (
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
                  <StatusBadge status={pub.status} />
                </div>
              </li>
            ))}
          </ul>
        ) : null}
      </StateView>

      <ConfirmDialog
        open={retryTarget != null}
        onClose={() => setRetryTarget(null)}
        onConfirm={() => void confirmRetry()}
        title="Reintentar publicación"
        message={
          retryTarget
            ? retryPublication.error
              ? `${retryPublication.error.message} Probá más tarde o revisá los logs.`
              : `Se re-encolará el job de publicación para "${retryTarget.clip.title} (${retryTarget.platform})".`
            : ''
        }
        confirmLabel="Reintentar"
        loading={retryPublication.isPending}
      />
    </Card>
  )
}