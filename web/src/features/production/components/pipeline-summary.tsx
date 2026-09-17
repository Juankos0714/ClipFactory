import { Link } from 'react-router-dom'
import { useSystemOverview } from '@/hooks/use-system'
import { clipsBy, jobsBy, publicationsBy, sourceClipsBy } from '@/lib/overview'
import { MetricCard } from '@/components/shared/metric-card'
import { Card, CardHeader } from '@/components/ui/card'
import { StateView } from '@/components/ui/state'

/**
 * Conteos del pipeline por etapa. Derivados de GET /api/system/overview (el
 * backend agrega las mismas tablas que listan /api/clips y /api/publications),
 * para no encender N queries de conteo por estado.
 */
export function PipelineSummary() {
  const { data, isLoading, error, refetch } = useSystemOverview()

  return (
    <Card>
      <CardHeader
        id="pipeline"
        title="Pipeline de publicación"
        action={
          <Link to="/production/queue" className="text-sm font-medium text-brand hover:underline">
            Ver cola de trabajos
          </Link>
        }
      />
      <StateView isLoading={isLoading} error={error} onRetry={() => void refetch()} loadingRows={2}>
        {data ? (
          <div className="grid grid-cols-2 gap-3 p-4 md:grid-cols-3 xl:grid-cols-6">
            <MetricCard label="Detectados" value={sourceClipsBy(data, 'detected')} accent="blue" hint="source_clips detectados" />
            <MetricCard label="Descargados" value={sourceClipsBy(data, 'downloaded')} accent="neutral" hint="listos para procesar" />
            <MetricCard label="Procesando" value={clipsBy(data, 'processing')} accent="amber" />
            <MetricCard label="Completados" value={clipsBy(data, 'completed')} accent="green" hint="clips verticales listos" />
            <MetricCard label="Publicados" value={publicationsBy(data, 'published')} accent="green" />
            <MetricCard label="Jobs en error" value={jobsBy(data, 'error')} accent="red" hint="revisar en la cola" />
          </div>
        ) : null}
      </StateView>
    </Card>
  )
}