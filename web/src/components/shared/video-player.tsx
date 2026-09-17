import { useMemo, useState } from 'react'
import { Camera, VideoOff } from 'lucide-react'
import { Button } from '@/components/ui/button'

/**
 * Reproductor de video reutilizable (contrato §3.5–3.6).
 * - Usa streaming nativo (byte-range) con <video>/<source>; la API obtiene el
 *   archivo, el navegador lo streamea. No se descarga a memoria.
 * - `src`/`poster` son URLs ya resueltas por los helpers de `clipsApi`.
 * - Sin src → placeholder honesto ("endpoint de stream requerido").
 */
export function VideoPlayer({
  src,
  poster,
  title,
}: {
  src: string | null
  poster?: string | null
  title: string
}) {
  const [failed, setFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)

  const videoUrl = src
  const posterUrl = poster ?? undefined

  const key = useMemo(() => `${videoUrl ?? 'none'}#${attempt}`, [videoUrl, attempt])

  if (!videoUrl) {
    return (
      <div className="flex aspect-video w-full flex-col items-center justify-center gap-2 rounded-lg bg-surface p-4 text-center">
        <VideoOff aria-hidden="true" className="h-8 w-8 text-neutral-500" />
        <p className="text-sm text-neutral-500">No hay archivo de video disponible.</p>
        <p className="text-xs text-neutral-600">
          El endpoint de stream (FRONTEND REQUIRES BACKEND ENDPOINT) expone el archivo.
        </p>
      </div>
    )
  }

  return (
    <div className="relative aspect-video w-full overflow-hidden rounded-lg bg-black">
      {failed ? (
        <div className="flex h-full flex-col items-center justify-center gap-2 p-4 text-center">
          <Camera aria-hidden="true" className="h-8 w-8 text-neutral-500" />
          <p className="text-sm text-neutral-400">No se pudo reproducir el video.</p>
          <Button
            variant="secondary"
            size="sm"
            onClick={() => {
              setAttempt((a) => a + 1)
              setFailed(false)
            }}
          >
            Reintentar
          </Button>
        </div>
      ) : (
        <video
          key={key}
          className="h-full w-full"
          controls
          preload="metadata"
          poster={posterUrl}
          aria-label={title}
          onError={() => setFailed(true)}
        >
          <source src={videoUrl} type="video/mp4" />
        </video>
      )}
    </div>
  )
}