import { useParams, Link } from 'react-router-dom'
import { ArrowLeft, Download, EyeOff } from 'lucide-react'
import { useClipDetail, useQueueForDownload } from '@/hooks/use-clips'
import { clipsApi } from '@/lib/api/resources'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardHeader } from '@/components/ui/card'
import { StateView } from '@/components/ui/state'
import { VideoPlayer } from '@/components/shared/video-player'
import { formatDateTime, formatDuration } from '@/lib/format'

export function ClipDetailPage() {
  const { clipId } = useParams<{ clipId: string }>()
  const id = clipId ? Number(clipId) : null
  const { data, isLoading, error, refetch } = useClipDetail(id)
  const queueForDownload = useQueueForDownload()

  if (isLoading || error || !data) {
    return (
      <StateView
        isLoading={isLoading}
        error={error}
        onRetry={() => void refetch()}
        emptyTitle="Clip no encontrado"
        loadingRows={6}
      >
        <></>
      </StateView>
    )
  }

  const { source_clip, video, clip, channel, publications, metrics } = data
  const previewSrc =
    clip?.status === 'completed' ? clipsApi.processedUrl(source_clip.id) : clipsApi.videoUrl(source_clip.id)
  const previewPoster = clip?.thumbnail_path ? clipsApi.thumbnailUrl(source_clip.id) : null
  const canDownload = source_clip.status === 'detected'

  return (
    <div className="flex flex-col gap-4">
      <Link to="/clips" className="inline-flex w-fit items-center gap-1.5 text-sm font-medium text-neutral-400 hover:text-neutral-100">
        <ArrowLeft aria-hidden="true" className="h-4 w-4" />
        Volver a clips
      </Link>

      <Card className="p-4">
        <div className="flex flex-col gap-2 md:flex-row md:items-start md:justify-between">
          <div>
            <h2 className="text-base font-semibold text-neutral-100">{source_clip.title}</h2>
            <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-neutral-500">
              <Badge>{channel.platform}</Badge>
              <span>{channel.channel_name}</span>
              <span>·</span>
              <span>{source_clip.platform_clip_id}</span>
              <span>·</span>
              <span>detectado {formatDateTime(source_clip.created_at_platform)}</span>
              <span>·</span>
              <span>{formatDuration(source_clip.duration_seconds)}</span>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <StatusBadge status={source_clip.status} />
            {canDownload ? (
              <Button
                size="sm"
                loading={queueForDownload.isPending}
                onClick={() => void queueForDownload.mutateAsync(source_clip.id)}
              >
                <Download aria-hidden="true" className="h-4 w-4" />
                Encolar descarga
              </Button>
            ) : null}
          </div>
        </div>

        <div className="mt-4">
          <VideoPlayer
            title={source_clip.title}
            src={previewSrc}
            poster={previewPoster}
          />
        </div>

        {!metrics.available ? (
          <p className="mt-3 rounded-md border border-border bg-surface px-3 py-2 text-xs text-neutral-500">
            Métricas de rendimiento (views/engagement) no disponibles: requieren recolección de métricas en el
            backend (BACKLOG del contrato).
          </p>
        ) : (
          <p className="mt-3 text-xs text-neutral-500">
            Views: {metrics.views ?? '—'} · views/h: {metrics.views_per_hour ?? '—'}
          </p>
        )}
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader id="pipeline" title="Pipeline" />
          <ul className="divide-y divide-border text-sm">
            <li className="flex items-center justify-between px-4 py-2.5">
              <span className="text-neutral-400">Archivo de origen</span>
              <StatusBadge status={video?.status ?? '—'} />
            </li>
            <li className="flex items-center justify-between px-4 py-2.5">
              <span className="text-neutral-400">Clip vertical 1080×1920</span>
              <StatusBadge status={clip?.status ?? '—'} />
            </li>
            <li className="flex items-center justify-between px-4 py-2.5">
              <span className="text-neutral-400">Miniatura</span>
              <span className="text-neutral-400">{clip?.thumbnail_path ? 'generada' : '—'}</span>
            </li>
            {source_clip.error_message ? (
              <li className="flex items-center justify-between gap-2 px-4 py-2.5 text-red-400">
                <EyeOff aria-hidden="true" className="h-4 w-4 shrink-0" />
                <span>{source_clip.error_message}</span>
              </li>
            ) : null}
          </ul>
        </Card>

        <Card>
          <CardHeader id="publications" title="Publicaciones" />
          {publications.length === 0 ? (
            <p className="px-4 py-3 text-sm text-neutral-500">Sin publicaciones de este clip todavía.</p>
          ) : (
            <ul className="divide-y divide-border text-sm">
              {publications.map((pub) => (
                <li key={pub.id} className="flex items-center justify-between gap-3 px-4 py-2.5">
                  <div>
                    <p className="font-medium capitalize text-neutral-100">{pub.platform}</p>
                    {pub.external_url ? (
                      <a
                        href={pub.external_url}
                        target="_blank"
                        rel="noreferrer"
                        className="text-xs text-brand hover:underline"
                      >
                        Ver publicación ↗
                      </a>
                    ) : pub.error_message ? (
                      <p className="text-xs text-red-400">{pub.error_message}</p>
                    ) : pub.next_retry_at ? (
                      <p className="text-xs text-neutral-500">
                        reintento {formatDateTime(pub.next_retry_at)} · intento {pub.attempts}
                      </p>
                    ) : null}
                  </div>
                  <StatusBadge status={pub.status} />
                </li>
              ))}
            </ul>
          )}
        </Card>
      </div>
    </div>
  )
}