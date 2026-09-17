import { clipsApi } from '@/lib/api/resources'
import { Dialog } from '@/components/ui/dialog'
import { StatusBadge } from '@/components/ui/badge'
import { VideoPlayer } from '@/components/shared/video-player'
import { formatDateTime, formatDuration } from '@/lib/format'
import type { ClipListItem } from '@/types/api'

/** Preview de un clip: stream del archivo según su etapa dentro del preview. */
export function ClipPreviewDialog({
  clip,
  onClose,
}: {
  clip: ClipListItem | null
  onClose: () => void
}) {
  const src = clip
    ? clip.clip?.status === 'completed'
      ? clipsApi.processedUrl(clip.source_clip.id)
      : clip.video
        ? clipsApi.videoUrl(clip.source_clip.id)
        : null
    : null
  const poster = clip?.clip?.thumbnail_path ? clipsApi.thumbnailUrl(clip.source_clip.id) : null

  return (
    <Dialog open={clip != null} onClose={onClose} title={clip?.source_clip.title ?? 'Vista previa'}>
      {clip ? (
        <div className="flex flex-col gap-3">
          <VideoPlayer title={clip.source_clip.title} src={src} poster={poster} />
          <div className="flex flex-wrap items-center gap-2 text-xs text-neutral-500">
            <StatusBadge status={clip.source_clip.status} />
            <span>{clip.channel.channel_name}</span>
            <span>·</span>
            <span>{formatDuration(clip.source_clip.duration_seconds)}</span>
            {clip.source_clip.created_at_platform ? (
              <span> · {formatDateTime(clip.source_clip.created_at_platform)}</span>
            ) : null}
          </div>
        </div>
      ) : null}
    </Dialog>
  )
}