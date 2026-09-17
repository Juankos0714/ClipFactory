import { useSystemOverview } from '@/hooks/use-system'
import { MetricCard } from '@/components/shared/metric-card'
import { StateView } from '@/components/ui/state'
import { Card, CardHeader } from '@/components/ui/card'
import { activeSources, clipsBy, jobsBy, publicationsBy, sourceClipsBy } from '@/lib/overview'

/** KPIs derivados SOLO de datos que la DB sabe contar (sin inventar views). */
export function MetricsOverview() {
  const { data, isLoading, error, refetch } = useSystemOverview()

  return (
    <Card>
      <CardHeader id="metrics" title="Métricas del pipeline" />
      <StateView isLoading={isLoading} error={error} onRetry={() => void refetch()} loadingRows={2}>
        {data ? (
          <div className="grid grid-cols-2 gap-3 p-4 md:grid-cols-3 xl:grid-cols-6">
            <MetricCard label="Canales activos" value={activeSources(data)} accent="blue" />
            <MetricCard label="Detectados" value={sourceClipsBy(data, 'detected')} accent="blue" />
            <MetricCard label="Completados" value={clipsBy(data, 'completed')} accent="green" />
            <MetricCard label="Publicados" value={publicationsBy(data, 'published')} accent="green" />
            <MetricCard label="Pendientes public." value={publicationsBy(data, 'pending')} accent="amber" />
            <MetricCard label="Jobs en error" value={jobsBy(data, 'error')} accent="red" />
          </div>
        ) : null}
      </StateView>
    </Card>
  )
}