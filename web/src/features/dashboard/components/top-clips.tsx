import { Link } from 'react-router-dom'
import { useClipsList } from '@/hooks/use-clips'
import { StatusBadge } from '@/components/ui/badge'
import { Card, CardHeader } from '@/components/ui/card'
import { StateView } from '@/components/ui/state'
import { formatDateTime, formatDuration } from '@/lib/format'

/**
 * Clips recientes. El ranking por views NO se fabrica: métricas de plataforma
 * son BACKLOG del contrato → se muestra un aviso honesto.
 */
export function TopClips() {
  const { data, isLoading, error, refetch } = useClipsList({ page: 1, pageSize: 5 })

  return (
    <Card>
      <CardHeader
        id="top-clips"
        title="Clips recientes"
        action={
          <Link to="/clips" className="text-sm font-medium text-brand hover:underline">
            Ver todos
          </Link>
        }
      />
      <StateView
        isLoading={isLoading}
        error={error}
        onRetry={() => void refetch()}
        isEmpty={(data?.data.length ?? 0) === 0}
        emptyTitle="Todavía no hay clips"
        emptyBody="Cuando el discovery detecte clips, aparecerán acá."
        loadingRows={4}
      >
        {data ? (
          <>
            <ul className="divide-y divide-border">
              {data.data.map((item) => (
                <li key={item.source_clip.id}>
                  <Link
                    to={`/clips/${item.source_clip.id}`}
                    className="grid grid-cols-2 items-center gap-2 px-4 py-3 hover:bg-surface md:grid-cols-[1fr_auto_auto_auto]"
                  >
                    <p className="truncate text-sm text-neutral-200" title={item.source_clip.title}>
                      {item.source_clip.title}
                    </p>
                    <p className="truncate text-xs text-neutral-500">{item.channel.channel_name}</p>
                    <StatusBadge status={item.source_clip.status} />
                    <p className="hidden text-xs text-neutral-500 md:block">
                      {formatDuration(item.source_clip.duration_seconds)} · {formatDateTime(item.source_clip.created_at_platform)}
                    </p>
                  </Link>
                </li>
              ))}
            </ul>
            <p className="border-t border-border px-4 py-2 text-xs text-neutral-500">
              Las vistas/engagement requieren el backend de métricas (BACKLOG del contrato).
            </p>
          </>
        ) : null}
      </StateView>
    </Card>
  )
}